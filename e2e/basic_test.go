//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestRunScript_NoRequirements(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "hello.py", "print('hello from invenv e2e')\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "-p", "python3", "--", script)

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if res.stdout != "hello from invenv e2e\n" {
		t.Errorf("stdout = %q, want %q", res.stdout, "hello from invenv e2e\n")
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want empty", res.stderr)
	}
}

func TestRunScript_PassesArgsAndEnvVars(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "echo_args.py", `import os
import sys
print("args=" + ",".join(sys.argv[1:]))
print("FOO=" + os.environ.get("FOO", "<missing>"))
`)
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "-p", "python3", "--", "FOO=bar", script, "one", "two")

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	want := "args=one,two\nFOO=bar\n"
	if res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want empty", res.stderr)
	}
}

func TestRunScript_ExitCodePropagates(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "exit7.py", "import sys\nsys.exit(7)\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "-p", "python3", "--", script)

	if res.exitCode != 7 {
		t.Errorf("exit code = %d, want 7 (stderr: %s)", res.exitCode, res.stderr)
	}
}

// TestRunScript_ShebangSelectsInterpreter exercises the real integration
// between extractPythonFromShebang/resolvePythonInterpreter and the actual
// exec — no -p flag, so invenv must read the shebang itself to pick python3.
func TestRunScript_ShebangSelectsInterpreter(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "shebang.py", "#!/usr/bin/env python3\nprint('ran via shebang interpreter')\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "--", script)

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if !strings.Contains(res.stdout, "ran via shebang interpreter") {
		t.Errorf("stdout = %q, want it to contain the script's output", res.stdout)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want empty", res.stderr)
	}
}
