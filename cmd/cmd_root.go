/*
Copyright © 2023 YAUHEN SHULITSKI <jsnjack@gmail.com>
*/
package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"syscall"

	"github.com/spf13/cobra"
)

var flagDebug bool
var flagTrace bool
var flagSilent bool

// Version is set at build time via ldflags.
var Version = "dev"

// TraceLogPath is where --trace writes its output. Truncated on every start.
const TraceLogPath = "/tmp/invenv.log"

// stdout is the user-facing stdout channel — used for output the user
// explicitly asked for (version, --which path). Not a log.
var stdout = os.Stdout

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use: "invenv [invenv-flags] -- [VAR=val] python-script.py",
	Example: `invenv -- somepath/myscript.py
invenv -n -- somepath/myscript.py --version
invenv -r req.txt -- DEBUG=1 somepath/myscript.py`,
	Short: "a tool to automatically create and run your Python scripts in a virtual environment with installed dependencies. See https://github.com/jsnjack/invenv",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		cleanup := initLoggerFromFlags()
		defer cleanup()

		versionFlag, err := cmd.Flags().GetBool("version")
		if err != nil {
			return fmt.Errorf("read --version flag: %w", err)
		}

		deleteOldEnvFlag, err := cmd.Flags().GetBool("new-environment")
		if err != nil {
			return fmt.Errorf("read --new-environment flag: %w", err)
		}

		isWhichFlag, err := cmd.Flags().GetBool("which")
		if err != nil {
			return fmt.Errorf("read --which flag: %w", err)
		}

		requirementsFileFlag, err := cmd.Flags().GetString("requirements-file")
		if err != nil {
			return fmt.Errorf("read --requirements-file flag: %w", err)
		}

		pythonFlag, err := cmd.Flags().GetString("python")
		if err != nil {
			return fmt.Errorf("read --python flag: %w", err)
		}

		if versionFlag {
			if _, err := fmt.Fprintln(stdout, Version); err != nil {
				return fmt.Errorf("write version: %w", err)
			}
			return nil
		}

		if len(args) == 0 {
			cmd.SilenceUsage = false
			return fmt.Errorf("no script name provided")
		}

		envVars, scriptName, scriptArgs := organizeArgs(args)
		if scriptName == "" {
			cmd.SilenceUsage = false
			return fmt.Errorf("no script name provided")
		}

		printProgress("Removing stale environments...")
		if err := clearStaleEnvs(); err != nil {
			slog.Debug("clear stale environments", "err", err)
		}

		printProgress("Gathering information about script and environment...")
		script, err := NewScript(scriptName, pythonFlag, requirementsFileFlag)
		if err != nil {
			return fmt.Errorf("prepare script: %w", err)
		}

		if isWhichFlag {
			// The flag's contract (see --help): if the environment does not
			// exist yet, it is created with requirements installed, so the
			// printed path is usable immediately (e.g. for sourcing
			// bin/activate).
			printProgress("Ensuring virtual environment...")
			if err := script.EnsureEnv(deleteOldEnvFlag); err != nil {
				return fmt.Errorf("ensure virtual environment: %w", err)
			}
			if !flagDebug {
				// Clear all progress messages
				printProgress("")
			}
			if _, err := fmt.Fprintln(stdout, script.EnvDir); err != nil {
				return fmt.Errorf("write env dir: %w", err)
			}
			return nil
		}

		printProgress("Ensuring virtual environment...")
		if err := script.EnsureEnv(deleteOldEnvFlag); err != nil {
			return fmt.Errorf("ensure virtual environment: %w", err)
		}

		printProgress("Done! Running script...")
		if !flagDebug {
			// Clear all progress messages
			printProgress("")
		}

		// Flush the buffers to preserve the output order and avoid interference
		// between the script output and the invenv output
		if err := os.Stderr.Sync(); err != nil {
			trace("sync stderr", "err", err)
		}
		if err := os.Stdout.Sync(); err != nil {
			trace("sync stdout", "err", err)
		}

		// https://gobyexample.com/execing-processes
		// Generate the command slice
		cmdSlice := append([]string{path.Join(script.EnvDir, "bin/python")}, scriptName)
		cmdSlice = append(cmdSlice, scriptArgs...)

		// Generate the environment
		cmdEnv := os.Environ()
		cmdEnv = append(envVars, cmdEnv...)
		if err := syscall.Exec(path.Join(script.EnvDir, "bin/python"), cmdSlice, cmdEnv); err != nil {
			return fmt.Errorf("exec python: %w", err)
		}
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

// initLoggerFromFlags wires the global slog logger from --debug / --trace.
// --trace takes precedence; both are independent of each other.
func initLoggerFromFlags() func() {
	level, tracePath := "", ""
	switch {
	case flagTrace:
		level, tracePath = "trace", TraceLogPath
	case flagDebug:
		level = "debug"
	}
	return initLogger(tracePath, level)
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&flagDebug, "debug", "d", false,
		"Debug-level logging on stderr.")
	rootCmd.PersistentFlags().BoolVar(&flagTrace, "trace", false,
		"Trace-level logs to "+TraceLogPath+" (truncated each run).")
	rootCmd.PersistentFlags().BoolVarP(&flagSilent, "silent", "s", false,
		"silence progress output. --debug flag overrides this")
	rootCmd.Flags().StringP("requirements-file", "r", "",
		`use specified requirements file. If not provided, it
will try to guess the requirements file name:
requirements_<script_name>.txt, <script_name>_requirements.txt or
requirements.txt`)
	rootCmd.Flags().BoolP("new-environment", "n", false, "create a new virtual environment even if it already exists")
	rootCmd.Flags().BoolP("which", "w", false,
		`print the location of virtual environment folder and exit. If
the virtual environment does not exist, it will be created with
installed requirements`)
	rootCmd.Flags().StringP("python", "p", "", "use specified Python interpreter")
	rootCmd.Flags().BoolP("version", "v", false, "print version and exit")
}
