package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fastPoll shortens the lockCheckInterval for the duration of a test so
// waitUntilEnvIsUnlocked loops run in milliseconds, not seconds.
func fastPoll(t *testing.T) {
	t.Helper()
	saved := lockCheckInterval
	lockCheckInterval = 5 * time.Millisecond
	t.Cleanup(func() { lockCheckInterval = saved })
}

// cleanupLock removes the lockfile, failing the test if removal errors.
// Used as a deferred cleanup to satisfy errcheck.
func cleanupLock(t *testing.T, envDir string) {
	t.Helper()
	if err := unlockEnv(envDir); err != nil {
		t.Errorf("unlockEnv cleanup: %v", err)
	}
}

func TestLockEnv_CreatesLockfile(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("lockEnv: %v", err)
	}
	defer cleanupLock(t, envDir)

	if !isEnvLocked(envDir) {
		t.Error("isEnvLocked = false after lockEnv")
	}
	if _, err := os.Stat(generateLockFileName(envDir)); err != nil {
		t.Errorf("lock file not on disk: %v", err)
	}
}

func TestLockEnv_AlreadyLocked(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("first lockEnv: %v", err)
	}
	defer cleanupLock(t, envDir)

	err := lockEnv(envDir)
	if !errors.Is(err, ErrEnvAlreadyLocked) {
		t.Errorf("second lockEnv: got %v, want ErrEnvAlreadyLocked", err)
	}
}

func TestLockEnv_CreatesParentDir(t *testing.T) {
	// envDir is under a non-existent parent; lockEnv should create the parent.
	envDir := filepath.Join(t.TempDir(), "nested", "subdir", "foo.env")
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("lockEnv: %v", err)
	}
	defer cleanupLock(t, envDir)

	if !isEnvLocked(envDir) {
		t.Error("isEnvLocked = false after lockEnv in nested path")
	}
}

func TestUnlockEnv_RemovesLockfile(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("lockEnv: %v", err)
	}
	if err := unlockEnv(envDir); err != nil {
		t.Fatalf("unlockEnv: %v", err)
	}
	if isEnvLocked(envDir) {
		t.Error("isEnvLocked = true after unlockEnv")
	}
}

func TestUnlockEnv_MissingLockIsNotAnError(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	// No prior lockEnv. Should be idempotent.
	if err := unlockEnv(envDir); err != nil {
		t.Errorf("unlockEnv on missing lock: got %v, want nil", err)
	}
}

func TestIsEnvLocked_FalseWhenAbsent(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	if isEnvLocked(envDir) {
		t.Error("isEnvLocked = true with no lockfile present")
	}
}

func TestLockEnv_ConcurrentExactlyOneWinner(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	const N = 50
	var wg sync.WaitGroup
	var wins int32
	var alreadyLocked int32

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := lockEnv(envDir)
			switch {
			case err == nil:
				atomic.AddInt32(&wins, 1)
			case errors.Is(err, ErrEnvAlreadyLocked):
				atomic.AddInt32(&alreadyLocked, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	defer cleanupLock(t, envDir)

	if wins != 1 {
		t.Errorf("winners: got %d, want exactly 1", wins)
	}
	if int(wins+alreadyLocked) != N {
		t.Errorf("accounted-for: got %d, want %d", wins+alreadyLocked, N)
	}
}

func TestLockEnv_RelockAfterUnlock(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := unlockEnv(envDir); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("relock after unlock: %v", err)
	}
	defer cleanupLock(t, envDir)
}

func TestWaitUntilEnvIsUnlocked_NoLockReturnsImmediately(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	start := time.Now()
	if err := waitUntilEnvIsUnlocked(envDir); err != nil {
		t.Errorf("got %v, want nil", err)
	}
	// "Immediate" with margin for CI noise: well under one poll interval.
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("returned too slowly: %v", elapsed)
	}
}

// TestWaitUntilEnvIsUnlocked_LinuxStaleByNoMatchingProcess documents the
// CURRENT behaviour on Linux: when a lockfile exists but no process's
// /proc/<pid>/cmdline starts with envDir, the lock is flagged stale
// immediately and ErrNoProcessFound is returned.
//
// This is bug B1 in the review: during the venv-creation window, the
// invenv parent is alive but its cmdline doesn't start with envDir, so the
// stale detection misfires. Phase 2 replaces the /proc-prefix check with
// PID-based liveness; when that lands this test will need to be updated.
func TestWaitUntilEnvIsUnlocked_LinuxStaleByNoMatchingProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires /proc")
	}
	fastPoll(t)

	envDir := filepath.Join(t.TempDir(), "stale.env")
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("lockEnv: %v", err)
	}
	defer cleanupLock(t, envDir)

	start := time.Now()
	err := waitUntilEnvIsUnlocked(envDir)
	if !errors.Is(err, ErrNoProcessFound) {
		t.Errorf("got %v, want ErrNoProcessFound", err)
	}
	// Must return on or shortly after the first poll, not after LockStaleTime.
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("returned too slowly: %v", elapsed)
	}
}

// TestWaitUntilEnvIsUnlocked_UnlockBeforeCallReturnsNil verifies that a
// transient lock — held briefly then released before the wait starts — is
// handled cleanly. This is the trivial "lock vanished before we looked"
// case; the function checks isEnvLocked before sleeping.
func TestWaitUntilEnvIsUnlocked_UnlockBeforeCallReturnsNil(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "transient.env")
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("lockEnv: %v", err)
	}
	if err := unlockEnv(envDir); err != nil {
		t.Fatalf("unlockEnv: %v", err)
	}
	if err := waitUntilEnvIsUnlocked(envDir); err != nil {
		t.Errorf("got %v, want nil", err)
	}
}
