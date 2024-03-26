/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "initialize a virtual environment in the current directory",
	Long: `Initialize a virtual environment in the current directory in .venv directory.
If requirements.txt or similar file is present, it will automatically
install the dependenciesfrom it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		deleteOldEnvFlag, err := cmd.Flags().GetBool("new-environment")
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

		printProgress("Gathering information about script and environment...")
		script, err := NewInitCmd(pythonFlag, requirementsFileFlag)
		if err != nil {
			return err
		}

		printProgress("Ensuring virtual environment...")
		err = script.EnsureEnv(deleteOldEnvFlag)
		if err != nil {
			return err
		}

		printProgress("Done!")
		if !flagDebug {
			// Clear all progress messages
			printProgress("")
		}

		// Flush the buffers to preserve the output order and avoid interference
		// between the script output and the invenv output
		os.Stderr.Sync()
		os.Stdout.Sync()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringP("requirements-file", "r", "",
		`use specified requirements file. If not provided, it
will try to guess the requirements file name:
requirements_<script_name>.txt, <script_name>_requirements.txt or
requirements.txt`)
	initCmd.Flags().BoolP("new-environment", "n", false, "create a new virtual environment even if it already exists")
	initCmd.Flags().StringP("python", "p", "", "use specified Python interpreter")
}
