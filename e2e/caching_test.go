//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	icmd "invenv/cmd"
)

// TestCaching_SecondRunReusesEnv guards the core caching value proposition:
// a second run against the same requirements/interpreter must reuse the
// already-built environment rather than rebuilding it.
func TestCaching_SecondRunReusesEnv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "six==1.16.0\n")
	script := writeScript(t, dir, "use_six.py", "import six\nprint('six', six.__version__)\n")
	h := newIsolatedHome(t)

	res1 := runInvenv(t, dir, h.env, "-p", "python3", "-w", "--", script)
	if res1.exitCode != 0 {
		t.Fatalf("first run (--which): exit %d, stderr:\n%s", res1.exitCode, res1.stderr)
	}
	envDir := strings.TrimSpace(res1.stdout)
	if envDir == "" {
		t.Fatal("first run: --which printed no path")
	}
	marker := filepath.Join(envDir, icmd.VEnvBuiltMarker)
	info1, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat marker after first run: %v", err)
	}

	res2 := runInvenv(t, dir, h.env, "-p", "python3", "-w", "--", script)
	if res2.exitCode != 0 {
		t.Fatalf("second run (--which): exit %d, stderr:\n%s", res2.exitCode, res2.stderr)
	}
	if got := strings.TrimSpace(res2.stdout); got != envDir {
		t.Errorf("second run env dir = %q, want %q (same env)", got, envDir)
	}
	info2, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat marker after second run: %v", err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("marker mtime changed (%v -> %v): env was rebuilt instead of reused", info1.ModTime(), info2.ModTime())
	}
}

// TestCaching_NewEnvironmentFlagForcesRebuild: -n must tear down and
// recreate the env even though it is already healthy. A sentinel file
// (rather than an mtime comparison) proves the rebuild happened, since
// mtime resolution on some filesystems is too coarse to trust here.
func TestCaching_NewEnvironmentFlagForcesRebuild(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "trivial.py", "print('ok')\n")
	h := newIsolatedHome(t)

	res1 := runInvenv(t, dir, h.env, "-p", "python3", "-w", "--", script)
	if res1.exitCode != 0 {
		t.Fatalf("first run: exit %d, stderr:\n%s", res1.exitCode, res1.stderr)
	}
	envDir := strings.TrimSpace(res1.stdout)
	sentinel := filepath.Join(envDir, "sentinel")
	if err := os.WriteFile(sentinel, []byte("still here?"), 0644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	res2 := runInvenv(t, dir, h.env, "-p", "python3", "-n", "-w", "--", script)
	if res2.exitCode != 0 {
		t.Fatalf("second run (-n): exit %d, stderr:\n%s", res2.exitCode, res2.stderr)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("sentinel survived -n rebuild (stat err: %v); env was reused instead of rebuilt", err)
	}
}

// TestCaching_DifferentRequirementsDifferentEnv: the env dir is keyed by a
// hash of requirements + python version, so two different requirements
// files for the same script must resolve to two different env dirs.
func TestCaching_DifferentRequirementsDifferentEnv(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "trivial.py", "print('ok')\n")
	h := newIsolatedHome(t)

	writeFile(t, dir, "requirements.txt", "six==1.16.0\n")
	res1 := runInvenv(t, dir, h.env, "-p", "python3", "-w", "--", script)
	if res1.exitCode != 0 {
		t.Fatalf("first run: exit %d, stderr:\n%s", res1.exitCode, res1.stderr)
	}

	writeFile(t, dir, "requirements.txt", "six==1.15.0\n")
	res2 := runInvenv(t, dir, h.env, "-p", "python3", "-w", "--", script)
	if res2.exitCode != 0 {
		t.Fatalf("second run: exit %d, stderr:\n%s", res2.exitCode, res2.stderr)
	}

	env1, env2 := strings.TrimSpace(res1.stdout), strings.TrimSpace(res2.stdout)
	if env1 == "" || env2 == "" {
		t.Fatalf("empty env dir: %q / %q", env1, env2)
	}
	if env1 == env2 {
		t.Errorf("different requirements hashed to the same env dir: %s", env1)
	}

	entries, err := os.ReadDir(h.envsDir())
	if err != nil {
		t.Fatalf("read envs dir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("envs dir has %d entries, want 2: %v", len(entries), entries)
	}
}
