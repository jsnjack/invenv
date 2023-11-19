package cmd

import (
	"bufio"
	"crypto/sha1"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/go-cmd/cmd"
)

const CyanColor = "\033[1;36m"
const ResetColor = "\033[0m"

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
	envDir := path.Join(homeDir, ".local", "invenv", finalComp)
	return envDir, nil
}

// extractPythonFromShebang extracts the interpreter path from a shebang
func extractPythonFromShebang(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#!") {
			interpreterPath := strings.TrimPrefix(line, "#!")
			// Two cases are possible: it could be a python interpreter or it could be
			// something like /usr/bin/env python
			// First we split the line by spaces
			split := strings.Split(interpreterPath, " ")
			if len(split) > 1 {
				// The last part must be python interpreter
				return split[len(split)-1], nil
			}
			return interpreterPath, nil
		}

		// Skip comments
		if strings.HasPrefix(line, "#") {
			continue
		}
		break
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", fmt.Errorf("shebang not found in the file")
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
				loggerErr.Println(line)
			case line, open := <-envCmd.Stderr:
				if !open {
					envCmd.Stderr = nil
					continue
				}
				loggerErr.Println(line)
			}
		}
	}()

	// Run and wait for Cmd to return, discard Status
	status := <-envCmd.Start()

	// Wait for goroutine to print everything
	<-doneChan
	if status.Exit != 0 {
		return fmt.Errorf("exit code: %d", status.Exit)
	}
	return nil
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

	if status.Exit != 0 {
		return fmt.Errorf("exit code: %d", status.Exit)
	}
	return nil
}

// organizeArgs organizes the arguments in three groups:
// - env variables
// - script name
// - script arguments
func organizeArgs(args []string) ([]string, string, []string) {
	var envVars []string
	var scriptName string
	var scriptArgs []string
	var foundName bool

	for _, el := range args {
		if !foundName && strings.Contains(el, "=") {
			envVars = append(envVars, el)
		} else if !foundName {
			scriptName = el
			foundName = true
		} else {
			scriptArgs = append(scriptArgs, el)
		}
	}
	return envVars, scriptName, scriptArgs
}

// printProgress prints a progress message
func printProgress(s string) {
	if !flagDebug {
		if !flagSilent {
			// Clear the line
			fmt.Fprint(os.Stderr, "\033[2K\r")
			fmt.Fprint(os.Stderr, CyanColor+s+ResetColor)
		}
	} else {
		loggerErr.Println(CyanColor + s + ResetColor)
	}
}

func removeDir(dir string) error {
	err := os.RemoveAll(dir)
	if err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			// Extreme case, try with sudo
			err = execCmd("sudo", "rm", "-rf", dir)
			if err != nil {
				return fmt.Errorf("failed to delete directory: %s", err)
			}
			return nil
		}
		return fmt.Errorf("failed to delete directory: %s", err)
	}
	return nil
}

func getPythonVersion(pythonInterpreter string) (string, error) {
	// Verify that the Python version used to create the virtual environment is the same
	// as the current Python version
	currentPythonVersion, err := exec.Command(pythonInterpreter, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get Python version: %s", err)
	}
	currentPythonVersionStr := strings.TrimSpace(string(currentPythonVersion))
	if flagDebug {
		loggerErr.Printf("Python interpreter %s has version %s\n", pythonInterpreter, currentPythonVersionStr)
	}
	return currentPythonVersionStr, nil
}
