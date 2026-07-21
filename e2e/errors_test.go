//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestErrors_MissingScript(t *testing.T) {
	dir := t.TempDir()
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "-p", "python3", "--", filepath.Join(dir, "does-not-exist.py"))

	if res.exitCode == 0 {
		t.Fatalf("expected non-zero exit, stdout:\n%s", res.stdout)
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want empty", res.stdout)
	}
	if res.stderr == "" {
		t.Error("stderr is empty, want an error message")
	}
}

func TestErrors_UnknownInterpreter(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "trivial.py", "print('ok')\n")
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env, "-p", "invenv-e2e-no-such-interpreter", "--", script)

	if res.exitCode == 0 {
		t.Fatalf("expected non-zero exit, stdout:\n%s", res.stdout)
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want empty", res.stdout)
	}
	if !strings.Contains(res.stderr, "invenv-e2e-no-such-interpreter") {
		t.Errorf("stderr = %q, want it to mention the missing interpreter", res.stderr)
	}
}

func TestErrors_NoScriptNameProvided(t *testing.T) {
	dir := t.TempDir()
	h := newIsolatedHome(t)

	res := runInvenv(t, dir, h.env)

	if res.exitCode == 0 {
		t.Fatalf("expected non-zero exit, stdout:\n%s", res.stdout)
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want empty", res.stdout)
	}
	if res.stderr == "" {
		t.Error("stderr is empty, want a usage/error message")
	}
}
