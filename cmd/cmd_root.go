/*
Copyright © 2023 YAUHEN SHULITSKI <jsnjack@gmail.com>
*/
package cmd

import (
	"fmt"
	"log"
	"os"
	"path"
	"syscall"

	"github.com/spf13/cobra"
)

var flagDebug bool
var flagSilent bool
var Version = "dev"

var loggerErr = log.New(os.Stderr, "", 0)
var loggerOut = log.New(os.Stdout, "", 0)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use: "invenv [invenv-flags] -- [VAR=val] python-script.py",
	Example: `invenv -- somepath/myscript.py
invenv -n -- somepath/myscript.py --version
invenv -r req.txt -- DEBUG=1 somepath/myscript.py`,
	Short: "a tool to automatically create and run your Python scripts in a virtual environment with installed dependencies. See https://github.com/jsnjack/invenv",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		// Extract the flags
		versionFlag, err := cmd.Flags().GetBool("version")
		if err != nil {
			return err
		}

		deleteOldEnvFlag, err := cmd.Flags().GetBool("new-environment")
		if err != nil {
			return err
		}

		isWhichFlag, err := cmd.Flags().GetBool("which")
		if err != nil {
			return err
		}

		requirementsFileFlag, err := cmd.Flags().GetString("requirements-file")
		if err != nil {
			return err
		}

		pythonFlag, err := cmd.Flags().GetString("python")
		if err != nil {
			return err
		}

		if versionFlag {
			loggerOut.Println(Version)
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

		printProgress("Parsing script file...")
		script, err := NewScript(scriptName, pythonFlag)
		if err != nil {
			return err
		}

		printProgress("Configuring virtual environment...")
		err = script.CreateEnv(deleteOldEnvFlag)
		if err != nil {
			return err
		}

		printProgress("Installing requirements...")
		// Requirements can be provided as arguments or we could try to guess
		// the requirements file name
		if requirementsFileFlag != "" {
			if !path.IsAbs(requirementsFileFlag) {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				requirementsFileFlag = path.Join(cwd, requirementsFileFlag)
			}
			err = script.InstallRequirementsInEnv(requirementsFileFlag)
			if err != nil {
				return err
			}
		} else {
			err = script.GuessAndInstallRequirements()
			if err != nil {
				return err
			}
		}

		if isWhichFlag {
			if !flagDebug {
				// Clear all progress messages
				printProgress("")
			}
			loggerOut.Println(script.EnvDir)
			return nil
		}

		printProgress("Done! Running script...")
		if !flagDebug {
			// Clear all progress messages
			printProgress("")
		}

		// Flush the buffers to preserve the output order and avoid interference
		// between the script output and the invenv output
		os.Stderr.Sync()
		os.Stdout.Sync()

		// https://gobyexample.com/execing-processes
		// Generate the command slice
		cmdSlice := append([]string{path.Join(script.EnvDir, "bin/python")}, scriptName)
		cmdSlice = append(cmdSlice, scriptArgs...)

		// Generate the environment
		cmdEnv := os.Environ()
		cmdEnv = append(envVars, cmdEnv...)
		return syscall.Exec(path.Join(script.EnvDir, "bin/python"), cmdSlice, cmdEnv)
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

func init() {
	rootCmd.PersistentFlags().BoolVarP(&flagDebug, "debug", "d", false, "enable debug mode with verbose output")
	rootCmd.PersistentFlags().BoolVarP(&flagSilent, "silent", "s", false, "silence progress output. --debug flag overrides this")
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
