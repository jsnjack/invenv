//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhichFlag_PrintsEnvDirAndBuildsIt(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "trivial.py", "print('ok')\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "-p", "python3", "-w", "--", script)

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	lines := strings.Split(strings.TrimRight(res.stdout, "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("stdout = %q, want exactly one path", res.stdout)
	}
	envDir := lines[0]
	if !strings.HasPrefix(envDir, h.envsDir()) {
		t.Errorf("printed path %q is not under the isolated cache dir %q", envDir, h.envsDir())
	}
	if _, err := os.Stat(filepath.Join(envDir, "bin", "python")); err != nil {
		t.Errorf("--which did not actually build the env: %v", err)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want empty", res.stderr)
	}
}

// TestVersionFlag_PrintsVersionOnlyAndTouchesNoState: --version is handled
// before any env/cache work happens (see cmd_root.go), so it must not
// create the environments dir as a side effect.
func TestVersionFlag_PrintsVersionOnlyAndTouchesNoState(t *testing.T) {
	dir := t.TempDir()
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "--version")

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if res.stdout != "dev\n" {
		t.Errorf("stdout = %q, want %q (this repo's binaries are built without -ldflags in this suite)", res.stdout, "dev\n")
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want empty", res.stderr)
	}
	if _, err := os.Stat(h.envsDir()); !os.IsNotExist(err) {
		t.Errorf("--version created the environments dir as a side effect (stat err: %v)", err)
	}
}
