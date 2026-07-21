//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestRequirements_InstallsAndScriptCanImport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "six==1.16.0\n")
	script := writeScript(t, dir, "use_six.py", "import six\nprint('six-version', six.__version__)\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "-p", "python3", "--", script)

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "six-version 1.16.0" {
		t.Errorf("stdout = %q, want %q", res.stdout, "six-version 1.16.0\n")
	}
}

// TestRequirements_InstallFailureCleansUpEnv: a failed pip install must not
// leave a broken environment directory behind for a later run to trip over.
func TestRequirements_InstallFailureCleansUpEnv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "this-package-definitely-does-not-exist-invenv-e2e==0.0.0\n")
	script := writeScript(t, dir, "unused.py", "print('should not run')\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "-p", "python3", "--", script)

	if res.exitCode == 0 {
		t.Fatalf("expected non-zero exit, stdout:\n%s", res.stdout)
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want empty (script must not have run)", res.stdout)
	}
	if res.stderr == "" {
		t.Error("stderr is empty, want a diagnostic message about the failed install")
	}

	entries, err := os.ReadDir(h.envsDir())
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read envs dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("broken env left behind after failed install: %v", entries)
	}
}
