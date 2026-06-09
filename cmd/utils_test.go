package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGetFileHash(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
		// Expected first-8-hex of SHA1, validated externally with `sha1sum`.
		want string
	}{
		{"empty", "", "da39a3ee"},
		{"hello", "hello", "aaf4c61d"},
		{"newline only", "\n", "adc83b19"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name)
			if err := os.WriteFile(p, []byte(tc.content), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := getFileHash(p)
			if err != nil {
				t.Fatalf("getFileHash: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetFileHash_MissingFile(t *testing.T) {
	_, err := getFileHash(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestGenerateEnvID_Deterministic(t *testing.T) {
	a := generateEnvID("abc12345", "Python 3.12.0")
	b := generateEnvID("abc12345", "Python 3.12.0")
	if a != b {
		t.Errorf("not deterministic: %q vs %q", a, b)
	}
	if a == "" {
		t.Error("env ID should be non-empty")
	}
}

func TestGenerateEnvID_DistinctInputs(t *testing.T) {
	cases := [][2]string{
		{"abc12345", "Python 3.12.0"},
		{"abc12346", "Python 3.12.0"},
		{"abc12345", "Python 3.12.1"},
		{"", "Python 3.12.0"},
		{"abc12345", ""},
	}
	seen := make(map[string][2]string)
	for _, c := range cases {
		id := generateEnvID(c[0], c[1])
		if prior, ok := seen[id]; ok {
			t.Errorf("collision between %v and %v → %q", prior, c, id)
		}
		seen[id] = c
	}
}

func TestGenerateLockFileName(t *testing.T) {
	cases := []struct {
		envDir string
		want   string
	}{
		{"/home/u/.local/invenv/abc.env", "/home/u/.local/invenv/abc.env.lock"},
		{"/tmp/.venv", "/tmp/.venv.lock"},
		{"bar.env", "bar.env.lock"},
	}
	for _, tc := range cases {
		t.Run(tc.envDir, func(t *testing.T) {
			got := generateLockFileName(tc.envDir)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOrganizeArgs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantEnvs   []string
		wantScript string
		wantArgs   []string
	}{
		{"only script", []string{"a.py"}, nil, "a.py", nil},
		{"vars + script", []string{"FOO=1", "BAR=2", "a.py"}, []string{"FOO=1", "BAR=2"}, "a.py", nil},
		{"script + args", []string{"a.py", "--flag", "x"}, nil, "a.py", []string{"--flag", "x"}},
		{"vars + script + args", []string{"FOO=1", "a.py", "--flag"}, []string{"FOO=1"}, "a.py", []string{"--flag"}},
		{"empty", nil, nil, "", nil},
		{"only vars (no script)", []string{"FOO=1"}, []string{"FOO=1"}, "", nil},
		{"= after script is a script arg", []string{"a.py", "X=Y"}, nil, "a.py", []string{"X=Y"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			envs, script, args := organizeArgs(tc.args)
			if !reflect.DeepEqual(envs, tc.wantEnvs) {
				t.Errorf("envs: got %v, want %v", envs, tc.wantEnvs)
			}
			if script != tc.wantScript {
				t.Errorf("script: got %q, want %q", script, tc.wantScript)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args: got %v, want %v", args, tc.wantArgs)
			}
		})
	}
}

// TestGetEnvironmentDir_RespectsXDGCacheHome verifies the env directory
// follows the XDG cache spec on Linux: XDG_CACHE_HOME (or ~/.cache when
// unset) + /invenv.
func TestGetEnvironmentDir_RespectsXDGCacheHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	got := getEnvironmentDir()
	want := filepath.Join(tmp, EnvironmentsDirName)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestGetEnvironmentDir_NotUnderDotLocal locks in the migration: env dir
// must not be under ~/.local/invenv anymore. Anyone reintroducing that
// path will see this test fail.
func TestGetEnvironmentDir_NotUnderDotLocal(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	got := getEnvironmentDir()
	if strings.Contains(got, ".local/invenv") {
		t.Errorf("env dir reverted to legacy location: %q", got)
	}
}

// TestGetEnvironmentDir_FallbackIsPerUser covers the no-$HOME case (e.g.
// cron): the fallback must be a per-user path under the temp dir, never a
// shared, predictable one another local user could pre-create.
func TestGetEnvironmentDir_FallbackIsPerUser(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("TMPDIR", tmp)
	got := getEnvironmentDir()
	want := filepath.Join(tmp, fmt.Sprintf("%s-%d", EnvironmentsDirName, os.Getuid()))
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractPythonFromShebang(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{"direct_path", "#!/usr/bin/python3\nprint('hi')\n", "/usr/bin/python3", false},
		{"direct_path_with_flags", "#!/usr/bin/python3 -u\nprint('hi')\n", "/usr/bin/python3", false},
		{"env_style", "#!/usr/bin/env python3\nprint('hi')\n", "python3", false},
		{"env_S_with_interpreter_flags", "#!/usr/bin/env -S python3 -u\n", "python3", false},
		{"env_S_with_var_assignment", "#!/usr/bin/env -S FOO=bar python3\n", "python3", false},
		{"bare_env_is_an_error", "#!/usr/bin/env\n", "", true},
		{"no_shebang", "print('hi')\n", "", true},
		{"blank_line_then_shebang", "\n#!/usr/bin/python3\n", "/usr/bin/python3", false},
		{"comment_then_shebang", "# coding: utf-8\n#!/usr/bin/python3\n", "/usr/bin/python3", false},
		{"empty_file", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_"))
			if err := os.WriteFile(p, []byte(tc.content), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := extractPythonFromShebang(p)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
