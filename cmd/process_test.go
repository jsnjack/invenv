package cmd

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// pollUntil polls fn every 5ms for up to timeout, returning as soon as fn
// returns true. A freshly-Start()ed process may still be running as a raw
// fork of the test binary for a brief window before its execve completes,
// so probing /proc immediately after Start() can race; this absorbs that.
func pollUntil(t *testing.T, timeout time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startSentinel starts a long-lived process with argv set explicitly
// (rather than derived from the binary path), so tests can control exactly
// what findProcessWithPrefix sees in /proc/[pid]/cmdline. It is killed and
// reaped on test cleanup.
func startSentinel(t *testing.T, argv []string) *exec.Cmd {
	t.Helper()
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not available: " + err.Error())
	}
	cmd := exec.Command(sleepPath, "5")
	cmd.Args = argv
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sentinel process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

// TestFindProcessWithPrefix_MatchesArgv0: a process whose argv[0] starts
// with the env dir (as invenv's syscall.Exec always sets it, see
// cmd_root.go) must be found.
func TestFindProcessWithPrefix_MatchesArgv0(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires /proc")
	}
	envDir := filepath.Join(t.TempDir(), "fake.env")
	argv0 := filepath.Join(envDir, "bin/python")
	cmd := startSentinel(t, []string{argv0, "5"})

	var pid int
	found := pollUntil(t, 500*time.Millisecond, func() bool {
		p, err := findProcessWithPrefix(envDir)
		if err != nil {
			return false
		}
		pid = p
		return true
	})
	if !found {
		t.Fatalf("findProcessWithPrefix never matched env dir %s", envDir)
	}
	if pid != cmd.Process.Pid {
		t.Errorf("got pid %d, want %d", pid, cmd.Process.Pid)
	}
}

// TestFindProcessWithPrefix_IgnoresLaterArguments guards the regression
// fix: a process that merely mentions the env dir in a later argument
// (e.g. a user inspecting a file inside it with cat/less/an editor) must
// not be mistaken for a process actually running as that env's python —
// only argv[0] identifies that.
func TestFindProcessWithPrefix_IgnoresLaterArguments(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires /proc")
	}
	envDir := filepath.Join(t.TempDir(), "fake.env")
	// argv[0] is unrelated to envDir; envDir only appears as argv[2].
	startSentinel(t, []string{"sleep", "5", envDir})

	// Give the exec a moment to land, then confirm it's never matched.
	time.Sleep(20 * time.Millisecond)
	_, err := findProcessWithPrefix(envDir)
	if !errors.Is(err, ErrNoProcessFound) {
		t.Errorf("findProcessWithPrefix: got err %v, want ErrNoProcessFound", err)
	}
}

// TestReadCmdlineArgv0_HandlesEmbeddedSpace: /proc/[pid]/cmdline separates
// arguments with null bytes, not spaces. An argv[0] that itself contains a
// space (env dir paths are not guaranteed to be space-free) must come back
// intact, not truncated at the space.
func TestReadCmdlineArgv0_HandlesEmbeddedSpace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires /proc")
	}
	dirWithSpace := filepath.Join(t.TempDir(), "env with space")
	argv0 := filepath.Join(dirWithSpace, "bin/python")
	cmd := startSentinel(t, []string{argv0, "5"})

	var got string
	found := pollUntil(t, 500*time.Millisecond, func() bool {
		argv0Read, err := readCmdlineArgv0(cmd.Process.Pid)
		if err != nil {
			return false
		}
		got = argv0Read
		return got == argv0
	})
	if !found {
		t.Fatalf("readCmdlineArgv0 = %q, want %q", got, argv0)
	}
}
