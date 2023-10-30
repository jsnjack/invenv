package cmd

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/go-cmd/cmd"
)

// generateEnvDirName generates a name for the directory that will contain the virtual environment
func generateEnvDirName(filename string) (string, error) {
	// Calculate hash of the script name
	hasher := sha256.New()
	hasher.Write([]byte(filename))
	hashBS := hasher.Sum(nil)
	hashStr := fmt.Sprintf("%x", hashBS)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// Env dir name contains hash of the script name (to support different locations),
	// and the script name itself (with dots replaced with dashes). Finally, it includes
	// the ".env" postfix.
	finalComp := hashStr + "_" + strings.ReplaceAll(path.Base(filename), ".", "-") + ".env"
	envDir := path.Join(homeDir, ".local", "ave", finalComp)
	return envDir, nil
}

// ensureDependency ensures that the dependency is installed
func ensureDependency(name string, arg ...string) error {
	err := execCmd(name, arg...)
	if err != nil {
		return fmt.Errorf("%s is not installed: %s", name, err)
	}
	return nil
}

func ensureAllDependencies() error {
	err := ensureDependency("python3", "--version")
	if err != nil {
		return err
	}

	err = ensureDependency("virtualenv", "--version")
	if err != nil {
		return err
	}

	err = ensureDependency("pip3", "--version")
	if err != nil {
		return err
	}
	return nil
}

// execCmd executes a command and streams its output to STDOUT and STDERR
func execCmd(name string, arg ...string) error {
	// Disable output buffering, enable streaming
	cmdOptions := cmd.Options{
		Buffered:  false,
		Streaming: true,
	}

	// Create Cmd with options
	envCmd := cmd.NewCmdOptions(cmdOptions, name, arg...)

	// Print STDOUT and STDERR lines streaming from Cmd
	doneChan := make(chan struct{})
	go func() {
		defer close(doneChan)
		for envCmd.Stdout != nil || envCmd.Stderr != nil {
			select {
			case line, open := <-envCmd.Stdout:
				if !open {
					envCmd.Stdout = nil
					continue
				}
				if flagDebug {
					fmt.Println(line)
				}
			case line, open := <-envCmd.Stderr:
				if !open {
					envCmd.Stderr = nil
					continue
				}
				if flagDebug {
					fmt.Fprintln(os.Stderr, line)
				}
			}
		}
	}()

	// Run and wait for Cmd to return, discard Status
	status := <-envCmd.Start()

	// Wait for goroutine to print everything
	<-doneChan
	return status.Error
}
