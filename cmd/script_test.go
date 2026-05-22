package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
