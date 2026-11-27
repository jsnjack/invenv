//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	icmd "invenv/cmd"
)

// printSix prints the installed six version; printPrefix prints the env
// path, which identifies which cached env the program ran in.
const (
	printSix    = "import six; print(six.__version__)"
	printPrefix = "import sys; print(sys.prefix)"
)

// TestREPL_PythonWithPackages: with no program named, repl starts the
// env's plain python with the --with packages installed. Like a script
// run, stdout carries only the program's output and stderr stays empty.
func TestREPL_PythonWithPackages(t *testing.T) {
	h := newIsolatedHome(t)

	res := runInvenv(t, t.TempDir(), h.env, "repl", "-p", "python3", "--with", "six==1.16.0", "--", "-c", printSix)

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if res.stdout != "1.16.0\n" {
		t.Errorf("stdout = %q, want %q", res.stdout, "1.16.0\n")
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want empty", res.stderr)
	}
}

// TestREPL_InstallsProgramPackage: naming a program installs the package
// of the same name and starts that program from the env.
func TestREPL_InstallsProgramPackage(t *testing.T) {
	h := newIsolatedHome(t)

	res := runInvenv(t, t.TempDir(), h.env, "repl", "ipython", "-p", "python3", "--with", "six==1.16.0", "--", "-c", printSix)

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if got := strings.TrimSpace(res.stdout); got != "1.16.0" {
		t.Errorf("ipython printed %q, want %q", got, "1.16.0")
	}
}

// TestREPL_SameEnvForAnyPackageOrder: the same packages listed in another
// order, or split across --with flags, reuse the cached env. The version
// specifier "pyflakes>=2,<4" also proves that the comma inside it is not
// read as a package separator (pip would reject "<4" on its own).
func TestREPL_SameEnvForAnyPackageOrder(t *testing.T) {
	h := newIsolatedHome(t)
	dir := t.TempDir()

	res1 := runInvenv(t, dir, h.env, "repl", "-p", "python3", "--with", "six==1.16.0,pyflakes>=2,<4", "--", "-c", printPrefix)
	if res1.exitCode != 0 {
		t.Fatalf("first run: exit %d, stderr:\n%s", res1.exitCode, res1.stderr)
	}
	envDir := strings.TrimSpace(res1.stdout)
	marker := filepath.Join(envDir, icmd.VEnvBuiltMarker)
	info1, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat marker after first run: %v", err)
	}

	res2 := runInvenv(t, dir, h.env, "repl", "-p", "python3", "--with", "pyflakes>=2,<4", "--with", "six==1.16.0", "--", "-c", printPrefix)
	if res2.exitCode != 0 {
		t.Fatalf("second run: exit %d, stderr:\n%s", res2.exitCode, res2.stderr)
	}
	if got := strings.TrimSpace(res2.stdout); got != envDir {
		t.Errorf("second run used env %q, want %q", got, envDir)
	}
	info2, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat marker after second run: %v", err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("marker mtime changed (%v -> %v): env was rebuilt instead of reused", info1.ModTime(), info2.ModTime())
	}
}

// TestREPL_UpgradeFlag: a cached env keeps the versions it was built with
// until -U upgrades them. The test pins six to an old release inside the
// env to stand in for "a newer nightly was published since the build".
func TestREPL_UpgradeFlag(t *testing.T) {
	h := newIsolatedHome(t)
	dir := t.TempDir()

	res := runInvenv(t, dir, h.env, "repl", "-p", "python3", "--with", "six", "--", "-c", printPrefix)
	if res.exitCode != 0 {
		t.Fatalf("build: exit %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	envDir := strings.TrimSpace(res.stdout)

	pin := exec.Command(filepath.Join(envDir, "bin", "pip"), "install", "--no-input", "-q", "six==1.15.0")
	pin.Env = h.env
	if out, err := pin.CombinedOutput(); err != nil {
		t.Fatalf("pin six==1.15.0: %v\n%s", err, out)
	}

	res = runInvenv(t, dir, h.env, "repl", "-p", "python3", "--with", "six", "--", "-c", printSix)
	if res.exitCode != 0 {
		t.Fatalf("run without -U: exit %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if got := strings.TrimSpace(res.stdout); got != "1.15.0" {
		t.Errorf("without -U six = %q, want the cached 1.15.0", got)
	}

	res = runInvenv(t, dir, h.env, "repl", "-p", "python3", "-U", "--with", "six", "--", "-c", printSix)
	if res.exitCode != 0 {
		t.Fatalf("run with -U: exit %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	if got := strings.TrimSpace(res.stdout); got == "1.15.0" || got == "" {
		t.Errorf("with -U six = %q, want a release newer than 1.15.0", got)
	}
	if res.stderr != "" {
		t.Errorf("stderr with -U = %q, want empty", res.stderr)
	}
}

// TestREPL_RejectsBadArguments: invalid arguments fail before any env is
// built.
func TestREPL_RejectsBadArguments(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"two programs", []string{"repl", "ipython", "bpython"}, "expected one program before --"},
		{"pip option as package", []string{"repl", "--with=-e."}, "not pip options"},
		{"path as program", []string{"repl", "./ipython"}, "must be a command name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newIsolatedHome(t)
			res := runInvenv(t, t.TempDir(), h.env, tt.args...)
			if res.exitCode == 0 {
				t.Fatalf("exit code = 0, want nonzero")
			}
			if !strings.Contains(res.stderr, tt.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", res.stderr, tt.wantErr)
			}
			if entries, err := os.ReadDir(h.envsDir()); err == nil && len(entries) > 0 {
				t.Errorf("an env was built in %s despite the error", h.envsDir())
			}
		})
	}
}
