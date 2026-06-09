package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// deadPID returns a PID that has just been reaped and is therefore
// guaranteed to not refer to a live process at the moment of return.
// There is a small theoretical race window during which the kernel could
// reassign this PID to a brand-new process before the test consults it;
// for short single-machine test runs that is effectively never.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sentinel process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait sentinel process: %v", err)
	}
	return pid
}

// writeLockFile bypasses lockEnv to inject controlled lockfile content
// (and optionally an old mtime) for testing isLockStale / waitUntilEnvIsUnlocked.
func writeLockFile(t *testing.T, envDir, content string, mtime time.Time) {
	t.Helper()
	lockPath := generateLockFileName(envDir)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(lockPath, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

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

// ----- Phase 2: PID-content lockfile + cross-platform liveness -----

func TestLockEnv_FileContainsPidAndTimestamp(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("lockEnv: %v", err)
	}
	defer cleanupLock(t, envDir)

	info, err := readLockInfo(envDir)
	if err != nil {
		t.Fatalf("readLockInfo: %v", err)
	}
	if info.pid != os.Getpid() {
		t.Errorf("pid: got %d, want %d", info.pid, os.Getpid())
	}
	if info.startNs == 0 {
		t.Error("startNs is 0; expected a populated timestamp")
	}
}

func TestIsPidAlive_OurOwn(t *testing.T) {
	if !isPidAlive(os.Getpid()) {
		t.Error("our own PID should be reported alive")
	}
}

func TestIsPidAlive_DeadChild(t *testing.T) {
	pid := deadPID(t)
	if isPidAlive(pid) {
		t.Errorf("reaped child PID %d should not be alive", pid)
	}
}

func TestIsPidAlive_InvalidPid(t *testing.T) {
	if isPidAlive(0) {
		t.Error("pid 0 should not be alive")
	}
	if isPidAlive(-1) {
		t.Error("pid -1 should not be alive")
	}
}

func TestReadLockInfo_Missing(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	_, err := readLockInfo(envDir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("got %v, want os.ErrNotExist", err)
	}
}

func TestReadLockInfo_Empty(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	writeLockFile(t, envDir, "", time.Time{})
	info, err := readLockInfo(envDir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if info.pid != 0 || info.startNs != 0 {
		t.Errorf("empty file: got %+v, want zero", info)
	}
}

func TestReadLockInfo_Malformed(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	writeLockFile(t, envDir, "garbage data here\n", time.Time{})
	info, err := readLockInfo(envDir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if info.pid != 0 || info.startNs != 0 {
		t.Errorf("malformed: got %+v, want zero (treated as empty)", info)
	}
}

func TestReadLockInfo_WellFormed(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	writeLockFile(t, envDir, "1234 5678\n", time.Time{})
	info, err := readLockInfo(envDir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if info.pid != 1234 || info.startNs != 5678 {
		t.Errorf("got %+v, want {1234, 5678}", info)
	}
}

func TestIsLockStale_NoLock(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	stale, err := isLockStale(envDir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if stale {
		t.Error("no lockfile should not be stale")
	}
}

// TestIsLockStale_EmptyOldFormatIsStale captures backward compat for
// lockfiles left behind by older invenv versions (which wrote an empty
// file). Those crashed and aren't ours; recover immediately.
func TestIsLockStale_EmptyOldFormatIsStale(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	writeLockFile(t, envDir, "", time.Now())
	stale, err := isLockStale(envDir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !stale {
		t.Error("empty old-format lockfile should be stale")
	}
}

func TestIsLockStale_DeadPidIsStale(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	pid := deadPID(t)
	writeLockFile(t, envDir, fmt.Sprintf("%d %d\n", pid, time.Now().UnixNano()), time.Now())
	stale, err := isLockStale(envDir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !stale {
		t.Errorf("dead PID %d should make the lock stale", pid)
	}
}

func TestIsLockStale_AlivePidFreshMtimeNotStale(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	writeLockFile(t, envDir, fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().UnixNano()), time.Now())
	stale, err := isLockStale(envDir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if stale {
		t.Error("alive PID + fresh mtime should not be stale")
	}
}

// TestIsLockStale_AlivePidOldMtimeIsStale covers PID-reuse: a process
// happens to have the same PID as a long-dead lock owner. Heartbeats
// would have refreshed mtime if it were ours; an old mtime means the
// alive PID is unrelated and we should recover.
func TestIsLockStale_AlivePidOldMtimeIsStale(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	writeLockFile(t, envDir,
		fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().UnixNano()),
		time.Now().Add(-2*LockStaleTime))
	stale, err := isLockStale(envDir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !stale {
		t.Error("alive PID with old mtime should be stale (PID-reuse safety net)")
	}
}

func TestWaitUntilEnvIsUnlocked_DeadOwner(t *testing.T) {
	fastPoll(t)
	envDir := filepath.Join(t.TempDir(), "foo.env")
	pid := deadPID(t)
	writeLockFile(t, envDir, fmt.Sprintf("%d %d\n", pid, time.Now().UnixNano()), time.Now())

	err := waitUntilEnvIsUnlocked(envDir)
	if !errors.Is(err, errStaleLockfile) {
		t.Errorf("got %v, want errStaleLockfile", err)
	}
}

func TestWaitUntilEnvIsUnlocked_EmptyOldFormatStale(t *testing.T) {
	fastPoll(t)
	envDir := filepath.Join(t.TempDir(), "foo.env")
	writeLockFile(t, envDir, "", time.Now())

	err := waitUntilEnvIsUnlocked(envDir)
	if !errors.Is(err, errStaleLockfile) {
		t.Errorf("got %v, want errStaleLockfile", err)
	}
}

// TestWaitUntilEnvIsUnlocked_AliveOwnerBlocksThenReturns is the case the
// old /proc-prefix scan got wrong (bug B1). A lock owned by a live process
// whose cmdline doesn't start with envDir used to be flagged stale on the
// first poll. Now waitUntilEnvIsUnlocked actually waits.
func TestWaitUntilEnvIsUnlocked_AliveOwnerBlocksThenReturns(t *testing.T) {
	fastPoll(t)
	envDir := filepath.Join(t.TempDir(), "foo.env")
	writeLockFile(t, envDir,
		fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().UnixNano()),
		time.Now())

	const releaseAfter = 40 * time.Millisecond
	go func() {
		time.Sleep(releaseAfter)
		_ = os.Remove(generateLockFileName(envDir))
	}()

	start := time.Now()
	if err := waitUntilEnvIsUnlocked(envDir); err != nil {
		t.Errorf("got %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed < releaseAfter {
		t.Errorf("returned too quickly: %v < %v", elapsed, releaseAfter)
	}
}

// ----- Stale lock recovery -----

func TestClearStaleLock_RemovesStaleLock(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	pid := deadPID(t)
	writeLockFile(t, envDir, fmt.Sprintf("%d %d\n", pid, time.Now().UnixNano()), time.Now())

	if err := clearStaleLock(envDir); err != nil {
		t.Fatalf("clearStaleLock: %v", err)
	}
	if isEnvLocked(envDir) {
		t.Error("stale lock should have been removed")
	}
}

// TestClearStaleLock_KeepsFreshLock is the double-recovery race guard: a
// waiter that observed a stale lock must not remove the lock if another
// process recovered it and re-locked in the meantime.
func TestClearStaleLock_KeepsFreshLock(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	writeLockFile(t, envDir, fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().UnixNano()), time.Now())

	if err := clearStaleLock(envDir); err != nil {
		t.Fatalf("clearStaleLock: %v", err)
	}
	if !isEnvLocked(envDir) {
		t.Error("fresh lock owned by a live process must not be removed")
	}
}

func TestClearStaleLock_MissingLockIsBenign(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	if err := clearStaleLock(envDir); err != nil {
		t.Errorf("clearStaleLock on missing lock: got %v, want nil", err)
	}
}

// TestCleanupStaleEnv_SkipsActivelyLockedEnv: an env whose directory mtime
// is old but whose lock is held by a live process is mid-rebuild — cleanup
// must leave both the directory and the lock alone.
func TestCleanupStaleEnv_SkipsActivelyLockedEnv(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeLockFile(t, envDir, fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().UnixNano()), time.Now())

	if err := cleanupStaleEnv(envDir); err != nil {
		t.Fatalf("cleanupStaleEnv: %v", err)
	}
	if _, err := os.Stat(envDir); err != nil {
		t.Errorf("env dir removed while actively locked: %v", err)
	}
	if !isEnvLocked(envDir) {
		t.Error("live lock removed by stale cleanup")
	}
}

// TestCleanupStaleEnv_RemovesUnlockedEnv: with no lock and no process
// using the env, cleanup removes it. (Linux-only: the in-use probe reads
// /proc.)
func TestCleanupStaleEnv_RemovesUnlockedEnv(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("in-use probe requires /proc")
	}
	envDir := filepath.Join(t.TempDir(), "foo.env")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := cleanupStaleEnv(envDir); err != nil {
		t.Fatalf("cleanupStaleEnv: %v", err)
	}
	if _, err := os.Stat(envDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("env dir should have been removed, stat err: %v", err)
	}
}

// ----- Heartbeat -----

func TestHeartbeat_RefreshesMtime(t *testing.T) {
	saved := heartbeatInterval
	heartbeatInterval = 5 * time.Millisecond
	t.Cleanup(func() { heartbeatInterval = saved })

	envDir := filepath.Join(t.TempDir(), "foo.env")
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("lockEnv: %v", err)
	}
	defer cleanupLock(t, envDir)

	lockPath := generateLockFileName(envDir)
	info0, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	// Wait long enough for several heartbeats to fire.
	time.Sleep(40 * time.Millisecond)

	info1, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info1.ModTime().After(info0.ModTime()) {
		t.Errorf("mtime did not advance: before=%v after=%v", info0.ModTime(), info1.ModTime())
	}
}

func TestUnlockEnv_StopsHeartbeat(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "foo.env")
	if err := lockEnv(envDir); err != nil {
		t.Fatalf("lockEnv: %v", err)
	}
	if _, ok := heartbeats.Load(envDir); !ok {
		t.Fatal("heartbeat should be registered after lockEnv")
	}
	if err := unlockEnv(envDir); err != nil {
		t.Fatalf("unlockEnv: %v", err)
	}
	if _, ok := heartbeats.Load(envDir); ok {
		t.Error("heartbeat still registered after unlockEnv")
	}
}
