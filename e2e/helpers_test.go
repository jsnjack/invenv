//go:build e2e

// Package e2e drives the compiled invenv binary as a real subprocess —
// build, cache, install requirements, exec — instead of calling internal
// functions directly. It is gated behind the "e2e" build tag (see the
// `make e2e` target) because it needs network access (real "pip install"
// against PyPI) and takes much longer than the unit tests in cmd/, so it
// must never run as part of the default `make check`/`make test` loop.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	icmd "invenv/cmd"
)

// invenvPath is the path to the invenv binary built once in TestMain.
var invenvPath string

// pipCacheDir is shared across every isolated HOME in this run so repeated
// "pip install six==..." calls across tests hit a local cache instead of
// re-fetching from PyPI every time. It only speeds tests up; it does not
// weaken isolation, since each test still gets its own venv and its own
// XDG_CACHE_HOME for invenv's own env cache.
var pipCacheDir string

func TestMain(m *testing.M) {
	buildDir, err := os.MkdirTemp("", "invenv-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: create build dir:", err)
		os.Exit(1)
	}

	invenvPath = filepath.Join(buildDir, "invenv")
	build := exec.Command("go", "build", "-o", invenvPath, ".")
	build.Dir = ".." // repo root, where the main package lives
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: build invenv: %v\n%s\n", err, out)
		removeAll(buildDir)
		os.Exit(1)
	}

	pipCacheDir = filepath.Join(buildDir, "pip-cache")
	if err := os.MkdirAll(pipCacheDir, 0755); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: create pip cache dir:", err)
		removeAll(buildDir)
		os.Exit(1)
	}

	code := m.Run()
	removeAll(buildDir)
	os.Exit(code)
}

// removeAll cleans up the temp build dir best-effort; a leftover temp dir
// is harmless (it's under the OS temp dir), so a removal failure here is
// not worth failing the run over.
func removeAll(path string) {
	if err := os.RemoveAll(path); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: cleanup:", err)
	}
}

// isolatedHome is a private HOME + XDG_CACHE_HOME for one test, so tests
// never touch the real ~/.cache/invenv and never collide with each other
// or with a real invenv invocation running on the same machine.
type isolatedHome struct {
	env      []string
	cacheDir string // $XDG_CACHE_HOME
}

// newIsolatedHome sets up a private HOME/XDG_CACHE_HOME for the calling
// test. The overrides are prepended, not appended: on both glibc getenv
// and CPython os.environ the first occurrence of a duplicate key wins, so
// putting them first is what makes them actually take effect over any
// same-named vars already in this test process's own environment.
func newIsolatedHome(t *testing.T) isolatedHome {
	t.Helper()
	home := t.TempDir()
	cache := filepath.Join(home, "cache")
	if err := os.MkdirAll(cache, 0755); err != nil {
		t.Fatal(err)
	}
	overrides := []string{
		"HOME=" + home,
		"XDG_CACHE_HOME=" + cache,
		"PIP_CACHE_DIR=" + pipCacheDir,
	}
	return isolatedHome{env: append(overrides, os.Environ()...), cacheDir: cache}
}

// envsDir is where this home's invenv keeps its cached virtual
// environments — $XDG_CACHE_HOME/invenv/<id>.env.
func (h isolatedHome) envsDir() string {
	return filepath.Join(h.cacheDir, icmd.EnvironmentsDirName)
}

type runResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// runInvenv runs the built invenv binary with args, in dir, with env as its
// process environment. It fails the test outright if the process can't be
// started at all or times out; a normal nonzero exit is returned in
// exitCode, not treated as a test failure, since several tests exercise
// invenv's error paths on purpose.
func runInvenv(t *testing.T, dir string, env []string, args ...string) runResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, invenvPath, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("invenv %v timed out after 90s\nstdout:\n%s\nstderr:\n%s", args, stdout.String(), stderr.String())
	}

	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run invenv %v: %v", args, err)
		}
		exitCode = exitErr.ExitCode()
	}
	return runResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode}
}

// writeScript writes an executable file (a script invenv will run).
func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeFile writes a non-executable file (e.g. requirements.txt).
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
