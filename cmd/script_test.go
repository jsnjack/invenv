package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeFakeBinDir creates a directory containing a tiny executable shim for
// each given name and returns the directory path. Useful for controlling
// what's "available in $PATH" during interpreter-resolution tests.
func makeFakeBinDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho fake\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestResolvePythonInterpreter_OverrideExists(t *testing.T) {
	bin := makeFakeBinDir(t, "mypython")
	t.Setenv("PATH", bin)
	got, err := resolvePythonInterpreter("", "mypython")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "mypython" {
		t.Errorf("got %q, want %q", got, "mypython")
	}
}

func TestResolvePythonInterpreter_OverrideMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := resolvePythonInterpreter("", "python3.99-does-not-exist")
	if err == nil {
		t.Error("expected error for missing override, got nil")
	}
}

func TestResolvePythonInterpreter_NoScriptNoOverride(t *testing.T) {
	bin := makeFakeBinDir(t, "python")
	t.Setenv("PATH", bin)
	got, err := resolvePythonInterpreter("", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "python" {
		t.Errorf("got %q, want %q", got, "python")
	}
}

func TestResolvePythonInterpreter_ShebangExists(t *testing.T) {
	bin := makeFakeBinDir(t, "python3.10")
	t.Setenv("PATH", bin)
	scriptPath := filepath.Join(t.TempDir(), "script.py")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env python3.10\nprint('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolvePythonInterpreter(scriptPath, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "python3.10" {
		t.Errorf("got %q, want %q", got, "python3.10")
	}
}

// TestResolvePythonInterpreter_ShebangMissingWarnsAndFallsBack covers
// D6: the user's shebang requests a python that isn't installed; the
// resolver falls back to "python" but writes a stderr warning so the
// substitution isn't silent.
func TestResolvePythonInterpreter_ShebangMissingWarnsAndFallsBack(t *testing.T) {
	bin := makeFakeBinDir(t, "python")
	t.Setenv("PATH", bin)
	scriptPath := filepath.Join(t.TempDir(), "script.py")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env python3.99\nprint('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	saved := warningWriter
	warningWriter = &buf
	t.Cleanup(func() { warningWriter = saved })

	got, err := resolvePythonInterpreter(scriptPath, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "python" {
		t.Errorf("interpreter: got %q, want %q", got, "python")
	}
	warning := buf.String()
	if !strings.Contains(warning, "python3.99") {
		t.Errorf("warning missing requested interpreter: %q", warning)
	}
	if !strings.Contains(warning, "warning") {
		t.Errorf("warning missing the word 'warning': %q", warning)
	}
}

// TestResolvePythonInterpreter_NoFallbackAvailable: shebang interpreter is
// missing AND plain "python" isn't on PATH either. Should error, not panic.
func TestResolvePythonInterpreter_NoFallbackAvailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	scriptPath := filepath.Join(t.TempDir(), "script.py")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env python3.99\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	saved := warningWriter
	warningWriter = &buf
	t.Cleanup(func() { warningWriter = saved })

	_, err := resolvePythonInterpreter(scriptPath, "")
	if err == nil {
		t.Error("expected error when no python at all is available, got nil")
	}
}

// TestResolvePythonInterpreter_DefaultFallbackIsSilent: no shebang
// requested explicitly, no python in PATH — error, but no warning printed
// (we don't nag users who never named an interpreter to begin with).
func TestResolvePythonInterpreter_NoExplicitRequestNoWarning(t *testing.T) {
	bin := makeFakeBinDir(t, "python")
	t.Setenv("PATH", bin)

	var buf bytes.Buffer
	saved := warningWriter
	warningWriter = &buf
	t.Cleanup(func() { warningWriter = saved })

	got, err := resolvePythonInterpreter("", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "python" {
		t.Errorf("got %q, want %q", got, "python")
	}
	if buf.Len() > 0 {
		t.Errorf("unexpected warning when no explicit request was made: %q", buf.String())
	}
}

// TestNewScript_ResolvesAllFields exercises the full constructor path with
// a fake interpreter shim: requirements discovery, hashing, interpreter
// resolution, and env dir placement under the cache dir.
func TestNewScript_ResolvesAllFields(t *testing.T) {
	bin := makeFakeBinDir(t, "python")
	t.Setenv("PATH", bin)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "myscript.py")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env python\nprint('hi')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	reqPath := filepath.Join(dir, "requirements.txt")
	if err := os.WriteFile(reqPath, []byte("requests\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	script, err := NewScript(scriptPath, "", "")
	if err != nil {
		t.Fatalf("NewScript: %v", err)
	}
	if script.AbsolutePath != scriptPath {
		t.Errorf("AbsolutePath: got %q, want %q", script.AbsolutePath, scriptPath)
	}
	if script.RequirementsPath != reqPath {
		t.Errorf("RequirementsPath: got %q, want %q", script.RequirementsPath, reqPath)
	}
	if script.PythonInterpreter != "python" {
		t.Errorf("PythonInterpreter: got %q, want %q", script.PythonInterpreter, "python")
	}
	wantPrefix := filepath.Join(cache, EnvironmentsDirName) + string(os.PathSeparator)
	if !strings.HasPrefix(script.EnvDir, wantPrefix) {
		t.Errorf("EnvDir %q not under cache dir %q", script.EnvDir, wantPrefix)
	}
	if !strings.HasSuffix(script.EnvDir, ".env") {
		t.Errorf("EnvDir %q missing .env suffix", script.EnvDir)
	}
}

func TestNewScript_MissingScript(t *testing.T) {
	bin := makeFakeBinDir(t, "python")
	t.Setenv("PATH", bin)
	_, err := NewScript(filepath.Join(t.TempDir(), "nope.py"), "", "")
	if err == nil {
		t.Error("expected error for missing script, got nil")
	}
}

func TestCheckEnvHealth_Missing(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "does-not-exist")
	if got := checkEnvHealth(envDir); got != envMissing {
		t.Errorf("got %v, want envMissing", got)
	}
}

func TestCheckEnvHealth_BrokenEmptyDir(t *testing.T) {
	// Dir exists but no marker, no bin/python — partial build.
	envDir := filepath.Join(t.TempDir(), "broken.env")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		t.Fatal(err)
	}
	if got := checkEnvHealth(envDir); got != envBroken {
		t.Errorf("got %v, want envBroken", got)
	}
}

func TestCheckEnvHealth_BrokenWithSomeFilesButNoPython(t *testing.T) {
	// Dir has random junk but neither the marker nor bin/python.
	envDir := filepath.Join(t.TempDir(), "halfbuilt.env")
	if err := os.MkdirAll(filepath.Join(envDir, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "lib", "stub"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := checkEnvHealth(envDir); got != envBroken {
		t.Errorf("got %v, want envBroken", got)
	}
}

func TestCheckEnvHealth_HealthyWithMarker(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "healthy.env")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(envDir, VEnvBuiltMarker)
	if err := os.WriteFile(markerPath, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if got := checkEnvHealth(envDir); got != envHealthy {
		t.Errorf("got %v, want envHealthy", got)
	}
}

func TestTouchEnvOnUse_BumpsMtime(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "env")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Anchor mtime in the past.
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(dir, past, past); err != nil {
		t.Fatal(err)
	}
	info0, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	touchEnvOnUse(dir)

	info1, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info1.ModTime().After(info0.ModTime()) {
		t.Errorf("mtime did not advance: before=%v after=%v", info0.ModTime(), info1.ModTime())
	}
}

// TestTouchEnvOnUse_MissingDirIsBenign verifies the helper doesn't panic
// or surface an error when the dir is gone (e.g. removed between the
// success path and the deferred touch — a race we'd rather not crash on).
func TestTouchEnvOnUse_MissingDirIsBenign(t *testing.T) {
	touchEnvOnUse(filepath.Join(t.TempDir(), "does-not-exist"))
}

// TestCheckEnvHealth_LegacyEnvMigrates covers the one-time tax for users
// upgrading: an env from a pre-marker invenv version has bin/python but
// no marker. checkEnvHealth treats it as healthy and writes the marker so
// future checks are fast.
func TestCheckEnvHealth_LegacyEnvMigrates(t *testing.T) {
	envDir := filepath.Join(t.TempDir(), "legacy.env")
	binDir := filepath.Join(envDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "python"), nil, 0755); err != nil {
		t.Fatal(err)
	}

	if got := checkEnvHealth(envDir); got != envHealthy {
		t.Errorf("got %v, want envHealthy", got)
	}
	// Marker should now be on disk.
	if _, err := os.Stat(filepath.Join(envDir, VEnvBuiltMarker)); err != nil {
		t.Errorf("marker not written after legacy migration: %v", err)
	}

	// Second call exercises the fast path (marker present).
	if got := checkEnvHealth(envDir); got != envHealthy {
		t.Errorf("second call: got %v, want envHealthy", got)
	}
}
