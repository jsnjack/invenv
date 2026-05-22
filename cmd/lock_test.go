package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

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
