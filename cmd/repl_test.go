package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSplitPackageList(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"single", []string{"six"}, []string{"six"}},
		{"comma separated", []string{"a,b"}, []string{"a", "b"}},
		{"repeated flag", []string{"a", "b,c"}, []string{"a", "b", "c"}},
		{"trims spaces and empty items", []string{" a , ,b, "}, []string{"a", "b"}},
		{"keeps version clauses together", []string{"pkg>=1,<2"}, []string{"pkg>=1,<2"}},
		{"keeps clauses with spaces", []string{"pkg>=1, !=1.5, ~=1.2"}, []string{"pkg>=1, !=1.5, ~=1.2"}},
		{"splits after a version clause", []string{"a>=1,<2,b"}, []string{"a>=1,<2", "b"}},
		{"keeps extras together", []string{"pkg[x,y],b"}, []string{"pkg[x,y]", "b"}},
		{"empty", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitPackageList(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("splitPackageList(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestReplPackages_AddsProgramPackageExceptForPython(t *testing.T) {
	tests := []struct {
		program string
		want    []string
	}{
		{"ipython", []string{"ipython", "six"}},
		{"bpython", []string{"bpython", "six"}},
		{"python", []string{"six"}},
		{"python3", []string{"six"}},
		{"python3.12", []string{"six"}},
	}
	for _, tt := range tests {
		got, err := replPackages(tt.program, []string{"six"})
		if err != nil {
			t.Fatalf("replPackages(%q): %v", tt.program, err)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("replPackages(%q) = %q, want %q", tt.program, got, tt.want)
		}
	}
}

// The order of --with values must not change the env ID, or the same
// session started two ways would build two identical environments.
func TestReplPackages_OrderAndDuplicatesDoNotMatter(t *testing.T) {
	a, err := replPackages("ipython", []string{"webfuse-nightly", "requests,ipython"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := replPackages("ipython", []string{"requests", "webfuse-nightly"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a, b) {
		t.Errorf("same packages in a different order gave %q and %q", a, b)
	}
}

func TestReplPackages_RejectsPipOptions(t *testing.T) {
	for _, with := range []string{"-e .", "--index-url=https://example.com"} {
		if _, err := replPackages("python", []string{with}); err == nil {
			t.Errorf("replPackages accepted --with %q, want an error", with)
		}
	}
}

func TestSplitREPLArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		dash     int
		program  string
		progArgs []string
		wantErr  bool
	}{
		{"no args starts python", nil, -1, "python", nil, false},
		{"program only", []string{"ipython"}, -1, "ipython", nil, false},
		{"program and args", []string{"ipython", "-i", "x.py"}, 1, "ipython", []string{"-i", "x.py"}, false},
		{"args only starts python", []string{"-c", "print(1)"}, 0, "python", []string{"-c", "print(1)"}, false},
		{"two programs", []string{"ipython", "bpython"}, -1, "", nil, true},
		{"program args without dash", []string{"ipython", "x.py"}, -1, "", nil, true},
		{"path instead of command", []string{"./bin/ipython"}, -1, "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			program, progArgs, err := splitREPLArgs(tt.args, tt.dash)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if program != tt.program || !slices.Equal(progArgs, tt.progArgs) {
				t.Errorf("got (%q, %q), want (%q, %q)", program, progArgs, tt.program, tt.progArgs)
			}
		})
	}
}

// A repl env with the same packages as a script's requirements file must
// share that script's env instead of building a second identical one.
func TestReplRequirementsHash_MatchesScriptRequirementsFile(t *testing.T) {
	reqFile := filepath.Join(t.TempDir(), "requirements.txt")
	if err := os.WriteFile(reqFile, []byte("ipython\nsix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := getFileHash(reqFile)
	if err != nil {
		t.Fatal(err)
	}

	got, err := replRequirementsHash([]string{"ipython", "six"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("packages hash = %q, want the requirements file hash %q", got, want)
	}

	gotFileOnly, err := replRequirementsHash(nil, reqFile)
	if err != nil {
		t.Fatal(err)
	}
	if gotFileOnly != want {
		t.Errorf("-r only hash = %q, want %q", gotFileOnly, want)
	}
}

func TestReplRequirementsHash_EmptyMatchesNoRequirements(t *testing.T) {
	got, err := replRequirementsHash(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("hash with no packages = %q, want \"\" (a script with no requirements)", got)
	}
}

func TestReplRequirementsHash_IncludesRequirementsFile(t *testing.T) {
	dir := t.TempDir()
	reqA := filepath.Join(dir, "a.txt")
	reqB := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(reqA, []byte("requests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reqB, []byte("rich\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := replRequirementsHash([]string{"six"}, reqA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := replRequirementsHash([]string{"six"}, reqB)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("different requirements files gave the same hash")
	}
	if _, err := replRequirementsHash(nil, filepath.Join(dir, "missing.txt")); err == nil {
		t.Error("missing requirements file gave no error")
	}
}

func TestPipInstallArgs(t *testing.T) {
	script := &Script{RequirementsPath: "/r.txt"}
	if got, want := script.pipInstallArgs(false), []string{"install", "--no-input", "-r", "/r.txt"}; !slices.Equal(got, want) {
		t.Errorf("script args = %q, want %q", got, want)
	}

	repl := &Script{Packages: []string{"ipython", "six"}, RequirementsPath: "/r.txt"}
	want := []string{"install", "--no-input", "--upgrade", "ipython", "six", "-r", "/r.txt"}
	if got := repl.pipInstallArgs(true); !slices.Equal(got, want) {
		t.Errorf("repl upgrade args = %q, want %q", got, want)
	}
}
