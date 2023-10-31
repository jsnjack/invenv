package cmd

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/go-cmd/cmd"
)

// getFileHash calculates the SHA256 hash of the file
func getFileHash(filename string) (string, error) {
	// Check that the file exists
	_, err := os.Stat(filename)
	if err != nil {
		return "", err
	}

	dataBytes, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}

	// Calculate hash of the file
	hasher := sha1.New()
	hasher.Write(dataBytes)
	hashBS := hasher.Sum(nil)
	hashStr := fmt.Sprintf("%x", hashBS)[:8]
	return hashStr, nil
}

// generateEnvDirName generates a name for the directory that will contain the virtual environment
func generateEnvDirName(filename string) (string, error) {
	// Calculate hash of the script name
	hasher := sha1.New()
	hasher.Write([]byte(filename))
	hashBS := hasher.Sum(nil)
	hashStr := fmt.Sprintf("%x", hashBS)[:8]

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
	var err error
	if flagDebug {
		err = execCmd(name, arg...)
	} else {
		err = execCmdSilent(name, arg...)
	}
	if err != nil {
		return fmt.Errorf("%s is not installed: %s", name, err)
	}
	return nil
}

func ensureAllSystemDependencies() error {
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
				fmt.Println(line)
			case line, open := <-envCmd.Stderr:
				if !open {
					envCmd.Stderr = nil
					continue
				}
				fmt.Fprintln(os.Stderr, line)
			}
		}
	}()

	// Run and wait for Cmd to return, discard Status
	status := <-envCmd.Start()

	// Wait for goroutine to print everything
	<-doneChan
	return status.Error
}

// execCmdSilent executes a command and does not stream its output to STDOUT and STDERR
func execCmdSilent(name string, arg ...string) error {
	// Disable output buffering, enable streaming
	cmdOptions := cmd.Options{
		Buffered:  false,
		Streaming: false,
	}

	// Create Cmd with options
	envCmd := cmd.NewCmdOptions(cmdOptions, name, arg...)

	// Run and wait for Cmd to return, discard Status
	status := <-envCmd.Start()

	return status.Error
}

// printProgress prints a progress message
func printProgress(s string) {
	if !flagDebug {
		// Clear the line
		fmt.Print("\033[2K\r")
		fmt.Print(s)
	} else {
		fmt.Println(s)
	}
}
