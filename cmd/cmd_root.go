/*
Copyright © 2023 YAUHEN SHULITSKI <jsnjack@gmail.com>
*/
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var flagDebug bool

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "ave -- python-script.py",
	Short: "a tool to automatically create and activate a virtual environment for your Python script",
	Long:  ``,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		err := ensureAllDependencies()
		if err != nil {
			return err
		}

		script, err := NewScript(args[0])
		if err != nil {
			return err
		}

		err = script.CreateEnv()
		if err != nil {
			return err
		}

		err = script.InstallRequirements()
		if err != nil {
			return err
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

func init() {
	rootCmd.PersistentFlags().BoolVarP(&flagDebug, "debug", "d", false, "enable debug mode with verbose output")
}
