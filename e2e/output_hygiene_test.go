//go:build e2e

// invenv runs a lot in CI and other automated/non-interactive contexts,
// where stdout is often captured as the script's actual output (e.g. piped
// into another tool) and any unexpected bytes on stdout or stderr are, at
// best, confusing noise and, at worst, corrupt a caller's parsing. These
// tests pin down that contract: in default (non-tty) mode invenv itself
// must be silent, on both streams, regardless of whether the env is being
// built for the first time or reused from cache.
package e2e

import (
	"os"
	"strings"
	"testing"

	icmd "invenv/cmd"
)

// TestOutputHygiene_CleanOnFreshBuild: the first run against a script
// builds the venv and installs requirements. None of that — progress
// messages, pip's own output, venv creation output — must reach stdout or
// stderr when stderr is not a terminal (exactly the case here, and in CI).
func TestOutputHygiene_CleanOnFreshBuild(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "six==1.16.0\n")
	script := writeScript(t, dir, "use_six.py", "import six\nprint(six.__version__)\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "-p", "python3", "--", script)

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if res.stderr != "" {
		t.Errorf("stderr leaked output on a fresh build in default (CI-like, non-tty) mode: %q", res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "1.16.0" {
		t.Errorf("stdout = %q, want exactly the script's own output", res.stdout)
	}
}

// TestOutputHygiene_CleanOnCachedRun: same contract on the (far more
// common in CI) path where the env already exists and is just reused.
func TestOutputHygiene_CleanOnCachedRun(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "six==1.16.0\n")
	script := writeScript(t, dir, "use_six.py", "import six\nprint(six.__version__)\n")
	h := newIsolatedHome(t)

	priming := runInvenv(t, dir, h.env, "-p", "python3", "--", script)
	if priming.exitCode != 0 {
		t.Fatalf("priming run: exit %d, stderr:\n%s", priming.exitCode, priming.stderr)
	}

	res := runInvenv(t, dir, h.env, "-p", "python3", "--", script)

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if res.stderr != "" {
		t.Errorf("stderr leaked output on a cached run in default (CI-like, non-tty) mode: %q", res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "1.16.0" {
		t.Errorf("stdout = %q, want exactly the script's own output", res.stdout)
	}
}

// TestOutputHygiene_DebugFlagLogsGoToStderrNotStdout: --debug is an
// explicit opt-in to verbosity, so stderr SHOULD gain debug logs — but
// they must never leak onto stdout, which a CI caller may be parsing as
// the script's real output.
func TestOutputHygiene_DebugFlagLogsGoToStderrNotStdout(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "trivial.py", "print('script output')\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "--debug", "-p", "python3", "--", script)

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if !strings.Contains(res.stderr, "level=DEBUG") {
		t.Errorf("--debug produced no debug-level logs on stderr: %q", res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "script output" {
		t.Errorf("stdout = %q, want exactly the script's own output (debug logs must not leak onto stdout)", res.stdout)
	}
}

// TestOutputHygiene_TraceFlagKeepsStderrClean: --trace redirects logging to
// icmd.TraceLogPath instead of stderr. That path is a fixed, shared
// location (not per-test-isolated — it's a package constant, not
// configurable), so this test cannot run concurrently with another
// instance of itself or with a real --trace invocation on the same
// machine; it relies on this whole suite running sequentially (no
// t.Parallel() anywhere in this package).
func TestOutputHygiene_TraceFlagKeepsStderrClean(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "trivial.py", "print('script output')\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "--trace", "-p", "python3", "--", script)

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if res.stderr != "" {
		t.Errorf("--trace leaked onto stderr instead of the trace file: %q", res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "script output" {
		t.Errorf("stdout = %q, want exactly the script's own output", res.stdout)
	}
	traceBytes, err := os.ReadFile(icmd.TraceLogPath)
	if err != nil {
		t.Fatalf("read trace file %s: %v", icmd.TraceLogPath, err)
	}
	if len(traceBytes) == 0 {
		t.Error("trace file is empty")
	}
}
