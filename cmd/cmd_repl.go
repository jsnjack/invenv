package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// defaultREPL is the program `invenv repl` starts when none is named.
const defaultREPL = "python"

// replCmd represents the repl command
var replCmd = &cobra.Command{
	Use:   "repl [program] [flags] [-- program-args]",
	Short: "start an interactive Python session (REPL) with the given packages installed",
	Long: `Start an interactive Python session (a REPL: read-eval-print loop) in a
cached virtual environment with the packages from --with installed.

program is the REPL to start: python (the default), ipython, bpython,
ptpython or any other command. invenv also installs the package named after
the program, except for python, which needs none. Arguments after -- go to
the program.

The environment is keyed by the package list and the Python version, not by
the versions pip resolved, so later runs reuse it without upgrading. Pass -U
to upgrade the packages first, e.g. to get the newest nightly build.`,
	Example: `invenv repl
invenv repl ipython --with webfuse-nightly
invenv repl bpython --with requests,rich
invenv repl ipython -U --with webfuse-nightly
invenv repl python -r requirements.txt -- -i setup.py`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		cleanup := initLoggerFromFlags()
		defer cleanup()

		withFlag, err := cmd.Flags().GetStringArray("with")
		if err != nil {
			return fmt.Errorf("read --with flag: %w", err)
		}
		upgradeFlag, err := cmd.Flags().GetBool("upgrade")
		if err != nil {
			return fmt.Errorf("read --upgrade flag: %w", err)
		}
		requirementsFileFlag, err := cmd.Flags().GetString("requirements-file")
		if err != nil {
			return fmt.Errorf("read --requirements-file flag: %w", err)
		}
		pythonFlag, err := cmd.Flags().GetString("python")
		if err != nil {
			return fmt.Errorf("read --python flag: %w", err)
		}
		deleteOldEnvFlag, err := cmd.Flags().GetBool("new-environment")
		if err != nil {
			return fmt.Errorf("read --new-environment flag: %w", err)
		}

		program, programArgs, err := splitREPLArgs(args, cmd.ArgsLenAtDash())
		if err != nil {
			cmd.SilenceUsage = false
			return err
		}
		packages, err := replPackages(program, withFlag)
		if err != nil {
			return err
		}

		printProgress("Removing stale environments...")
		if err := clearStaleEnvs(); err != nil {
			slog.Debug("clear stale environments", "err", err)
		}

		printProgress("Gathering information about environment...")
		script, err := NewREPL(packages, pythonFlag, requirementsFileFlag)
		if err != nil {
			return fmt.Errorf("prepare environment: %w", err)
		}

		// An env built by this run already has the newest versions, so -U
		// only needs to run pip again on an env that existed before.
		builtNow := deleteOldEnvFlag || checkEnvHealth(script.EnvDir) != envHealthy

		printProgress("Ensuring virtual environment...")
		if err := script.EnsureEnv(deleteOldEnvFlag); err != nil {
			return fmt.Errorf("ensure virtual environment: %w", err)
		}

		if upgradeFlag && !builtNow {
			printProgress("Upgrading packages...")
			if err := script.UpgradeEnv(); err != nil {
				return fmt.Errorf("%w (run without -U to use the cached environment)", err)
			}
		}

		programPath := filepath.Join(script.EnvDir, "bin", program)
		if _, err := os.Stat(programPath); err != nil {
			return fmt.Errorf("find %s in the environment: %w", program, err)
		}

		printProgress("Done! Starting " + program + "...")
		if !flagDebug {
			// Clear all progress messages
			printProgress("")
		}

		argv := append([]string{programPath}, programArgs...)
		if err := execReplacingInvenv(programPath, argv, os.Environ()); err != nil {
			return fmt.Errorf("exec %s: %w", program, err)
		}
		return nil
	},
}

// splitREPLArgs returns the program to start and the arguments for it.
// dash is cobra's ArgsLenAtDash: the number of args before "--", or -1
// when there is no "--". Only the program name may come before "--".
func splitREPLArgs(args []string, dash int) (program string, programArgs []string, err error) {
	before := args
	if dash >= 0 {
		before, programArgs = args[:dash], args[dash:]
	}
	switch len(before) {
	case 0:
		program = defaultREPL
	case 1:
		program = before[0]
	default:
		return "", nil, fmt.Errorf("expected one program before --, got %q; put program arguments after --", before)
	}
	if program == "" || strings.ContainsRune(program, os.PathSeparator) {
		return "", nil, fmt.Errorf("program must be a command name such as ipython, not %q", program)
	}
	return program, programArgs, nil
}

// builtinPrograms matches the interpreters every venv provides (python,
// python3, python3.12), which need no package installed.
var builtinPrograms = regexp.MustCompile(`^python(3(\.[0-9]+)?)?$`)

// replPackages returns the requirement strings for a repl env: the
// package named after program (unless the venv already provides it) plus
// every --with value split by splitPackageList, sorted and deduplicated so
// that the order of --with values does not change the env ID.
func replPackages(program string, with []string) ([]string, error) {
	packages := splitPackageList(with)
	if !builtinPrograms.MatchString(program) {
		packages = append(packages, program)
	}
	for _, p := range packages {
		// pip would read a leading "-" as one of its own options.
		if strings.HasPrefix(p, "-") {
			return nil, errors.New("--with takes package names, not pip options: " + p)
		}
	}
	slices.Sort(packages)
	return slices.Compact(packages), nil
}

// splitPackageList splits each --with value at the commas that separate
// packages. It keeps the commas that belong to one requirement: inside
// extras ("pkg[a,b]") and between version clauses ("pkg>=1,<2"), where the
// text after the comma starts with a comparison operator.
func splitPackageList(values []string) []string {
	var packages []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			packages = append(packages, s)
		}
	}
	for _, v := range values {
		depth, start := 0, 0
		for i := 0; i < len(v); i++ {
			switch v[i] {
			case '[':
				depth++
			case ']':
				depth = max(depth-1, 0)
			case ',':
				next := strings.TrimLeft(v[i+1:], " ")
				if depth == 0 && (next == "" || !strings.ContainsRune("<>=!~", rune(next[0]))) {
					add(v[start:i])
					start = i + 1
				}
			}
		}
		add(v[start:])
	}
	return packages
}

func init() {
	rootCmd.AddCommand(replCmd)
	replCmd.Flags().StringArray("with", nil,
		`install these packages; repeat the flag or separate packages with
commas (version specifiers such as "pkg>=1,<2" are kept whole)`)
	replCmd.Flags().BoolP("upgrade", "U", false,
		"upgrade the packages in a cached environment before starting")
	replCmd.Flags().StringP("requirements-file", "r", "", "also install the packages from this requirements file")
	replCmd.Flags().StringP("python", "p", "", "use specified Python interpreter")
	replCmd.Flags().BoolP("new-environment", "n", false, "create a new virtual environment even if it already exists")
}
