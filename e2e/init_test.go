//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	icmd "invenv/cmd"
)

func TestInitCommand_CreatesDotVenv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "six==1.16.0\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "-p", "python3", "init")

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", res.exitCode, res.stderr)
	}
	venvDir := filepath.Join(dir, icmd.VEnvDirDefaultName)
	if _, err := os.Stat(filepath.Join(venvDir, "bin", "python")); err != nil {
		t.Errorf("init did not create %s/bin/python: %v", icmd.VEnvDirDefaultName, err)
	}
	idBytes, err := os.ReadFile(filepath.Join(venvDir, icmd.VEnvInfoFilename))
	if err != nil {
		t.Fatalf("read %s: %v", icmd.VEnvInfoFilename, err)
	}
	if strings.TrimSpace(string(idBytes)) == "" {
		t.Error("venv id file is empty")
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want empty", res.stderr)
	}
}

// TestInitCommand_RebuildsOnRequirementsChange: init has no requirements
// hash in its path (the venv always lives at .venv), so it relies entirely
// on comparing the stored venv id to detect that requirements.txt changed
// and a rebuild is needed.
func TestInitCommand_RebuildsOnRequirementsChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "six==1.16.0\n")
	h := newIsolatedHome(t)

	res1 := runInvenv(t, dir, h.env, "-p", "python3", "init")
	if res1.exitCode != 0 {
		t.Fatalf("first init: exit %d, stderr:\n%s", res1.exitCode, res1.stderr)
	}
	idFile := filepath.Join(dir, icmd.VEnvDirDefaultName, icmd.VEnvInfoFilename)
	id1, err := os.ReadFile(idFile)
	if err != nil {
		t.Fatalf("read venv id after first init: %v", err)
	}

	writeFile(t, dir, "requirements.txt", "six==1.15.0\n")
	res2 := runInvenv(t, dir, h.env, "-p", "python3", "init")
	if res2.exitCode != 0 {
		t.Fatalf("second init: exit %d, stderr:\n%s", res2.exitCode, res2.stderr)
	}
	id2, err := os.ReadFile(idFile)
	if err != nil {
		t.Fatalf("read venv id after second init: %v", err)
	}
	if string(id1) == string(id2) {
		t.Error("venv id unchanged after requirements.txt changed; init did not rebuild")
	}
}
