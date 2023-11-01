/*
Copyright © 2023 YAUHEN SHULITSKI <jsnjack@gmail.com>
*/
package cmd

import (
	"fmt"
	"os"
	"path"
	"syscall"

	"github.com/spf13/cobra"
)

var flagDebug bool
var Version = "dev"

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "invenv [invenv-flags] -- [VAR=val] python-script.py",
	Short: "a tool to automatically create and run your Python scripts in a virtual environment with installed dependencies. See https://github.com/jsnjack/invenv",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		version, err := cmd.Flags().GetBool("version")
		if err != nil {
			return err
		}
		if version {
			fmt.Println(Version)
			return nil
		}

		envVars, scriptName, scriptArgs := organizeArgs(args)
		if scriptName == "" {
			cmd.SilenceUsage = false
			return fmt.Errorf("no script name provided")
		}

		printProgress("Parsing script file...")
		script, err := NewScript(scriptName)
		if err != nil {
			return err
		}

		deleteOldEnv, err := cmd.Flags().GetBool("new-environment")
		if err != nil {
			return err
		}

		printProgress("Configuring virtual environment...")
		err = script.CreateEnv(deleteOldEnv)
		if err != nil {
			return err
		}

		// Requirements can be provided as arguments or we could try to guess
		// the requirements file name
		requirementsFile, err := cmd.Flags().GetString("requirements-file")
		if err != nil {
			return err
		}

		printProgress("Installing requirements...")
		if requirementsFile != "" {
			if !path.IsAbs(requirementsFile) {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				requirementsFile = path.Join(cwd, requirementsFile)
			}
			err = script.InstallRequirementsInEnv(requirementsFile)
			if err != nil {
				return err
			}
		} else {
			err = script.GuessAndInstallRequirements()
			if err != nil {
				return err
			}
		}

		printProgress("Done! Running script...")
		if !flagDebug {
			// Clear all progress messages
			printProgress("")
		}

		// https://gobyexample.com/execing-processes
		// Generate the command slice
		cmdSlice := append([]string{path.Join(script.EnvDir, "bin/python")}, scriptName)
		cmdSlice = append(cmdSlice, scriptArgs...)

		// Generate the environment
		cmdEnv := os.Environ()
		cmdEnv = append(cmdEnv, envVars...)
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
	rootCmd.Flags().StringP("requirements-file", "r", "", "use specified requirements file")
	rootCmd.Flags().BoolP("new-environment", "n", false, "create a new virtual environment even if it already exists")
	rootCmd.Flags().BoolP("version", "v", false, "print version and exit")
}
