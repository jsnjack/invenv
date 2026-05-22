package cmd

import (
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

func TestExtractPythonFromShebang(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{"direct_path", "#!/usr/bin/python3\nprint('hi')\n", "/usr/bin/python3", false},
		{"env_style", "#!/usr/bin/env python3\nprint('hi')\n", "python3", false},
		{"no_shebang", "print('hi')\n", "", true},
		{"blank_line_then_shebang", "\n#!/usr/bin/python3\n", "/usr/bin/python3", false},
		{"comment_then_shebang", "# coding: utf-8\n#!/usr/bin/python3\n", "/usr/bin/python3", false},
		// Documents a known bug: `env -S` returns the last token, not the interpreter.
		// Fixing this is out of scope for the locking work; locking down the current
		// behaviour so the bug is loud the day someone tries to fix it.
		{"env_S_returns_last_arg", "#!/usr/bin/env -S python3 -u\n", "-u", false},
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
