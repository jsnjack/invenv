package cmd

import (
	"bufio"
	"crypto/sha1"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-cmd/cmd"
	"github.com/mattheath/base62"
)

const EnvironmentsDir = ".local/invenv"

const CyanColor = "\033[1;36m"
const ResetColor = "\033[0m"

// LockAcquireAttempts is the number of attempts to acquire the lock. Also
// correlates with the number of seconds to wait for the lock.
const LockAcquireAttempts = 300

// LockStaleTime is the time after which the lock is considered stale
const LockStaleTime = 15 * time.Minute

// StaleEnvironmentTime is the time after which the virtual environment is considered stale
const StaleEnvironmentTime = 14 * 24 * time.Hour

// errStaleLock is returned when the lockfile is stale - older than LockStaleTime
var errStaleLockfile = fmt.Errorf("stale lockfile")

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

// generateEnvID generates a unique name for the virtual environment based
// on the requirements file hash and the Python version
func generateEnvID(requirementsHash, pythonVersion string) string {
	venvID := fmt.Sprintf("%s_%s", requirementsHash, pythonVersion)
	// Encode it in base62
	bigInt := big.NewInt(0).SetBytes([]byte(venvID))
	encoded := base62.EncodeBigInt(bigInt)
	return encoded
}

func generateLockFileName(envDir string) string {
	lockFileName := path.Join(path.Dir(envDir), path.Base(envDir)+".lock")
	return lockFileName
}

func isEnvLocked(envDir string) bool {
	lockFileName := generateLockFileName(envDir)
	_, err := os.Stat(lockFileName)
	if err != nil && os.IsNotExist(err) {
		return false
	}
	return true
}

func lockEnv(envDir string) error {
	if flagDebug {
		loggerErr.Println("Locking virtual environment...")
	}
	lockFileName := generateLockFileName(envDir)
	_, err := os.Stat(lockFileName)
	if err == nil {
		// Already locked
		return nil
	}
	if os.IsNotExist(err) {
		if err = os.MkdirAll(path.Dir(lockFileName), 0755); err != nil {
			return err
		}
		_, err = os.Create(lockFileName)
		return err
	}
	return err
}

func unlockEnv(envDir string) error {
	if flagDebug {
		loggerErr.Println("Unlocking virtual environment...")
	}
	lockFileName := generateLockFileName(envDir)
	err := os.Remove(lockFileName)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func waitUntilEnvIsUnlocked(envDir string) error {
	if flagDebug {
		loggerErr.Println("Acquiring lock on virtual environment...")
		defer loggerErr.Println("Lock acquired")
	}
	now := time.Now()
	for {
		if !isEnvLocked(envDir) {
			return nil
		}
		time.Sleep(1 * time.Second)
		if time.Since(now) > LockStaleTime {
			return errStaleLockfile
		}
		// Lockfile is not stale but lets check if there is a process which uses this virtual environment
		if runtime.GOOS == "linux" {
			_, err := findProcessWithPrefix(envDir)
			if err == ErrNoProcessFound {
				return err
			}
		}
	}
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
func execCmdSilent(name string, arg ...string) ([]string, error) {
	// Disable output buffering, enable streaming
	cmdOptions := cmd.Options{
		CombinedOutput: true,
		Streaming:      false,
	}

	// Create Cmd with options
	envCmd := cmd.NewCmdOptions(cmdOptions, name, arg...)

	// Run and wait for Cmd to return, discard Status
	status := <-envCmd.Start()

	if status.Exit != 0 {
		return status.Stdout, fmt.Errorf("exit code: %d", status.Exit)
	}
	return nil, nil
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

// getRequirementsFileForScript returns the requirements file for the script
func getRequirementsFileForScript(scriptPath string, requirementsOverride string) (string, error) {
	scriptPath, err := filepath.Abs(scriptPath)
	if err != nil {
		return "", err
	}

	// Select requirements file. First check if the file provided in overrides exists
	if requirementsOverride != "" {
		if !path.IsAbs(requirementsOverride) {
			cwd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			return path.Join(cwd, requirementsOverride), nil
		} else {
			return requirementsOverride, nil
		}
	} else {
		// Find suitable requirements file based on name patterns
		scriptDir := path.Dir(scriptPath)
		scriptFile := path.Base(scriptPath)
		scriptFile = strings.TrimSuffix(scriptFile, ".py")
		guesses := []string{
			"requirements_" + scriptFile + ".txt",
			scriptFile + "_requirements.txt",
			"requirements.txt",
		}

		for _, guess := range guesses {
			possibleRequirementsFile := path.Join(scriptDir, guess)
			if flagDebug {
				loggerErr.Printf("Assuming requirements file %s...\n", possibleRequirementsFile)
			}
			_, err := os.Stat(possibleRequirementsFile)
			if err == nil {
				return possibleRequirementsFile, nil
			} else {
				if flagDebug {
					loggerErr.Println(err)
				}
			}
		}
	}
	return "", nil
}

// clearStaleEnvs removes stale virtual environments
func clearStaleEnvs() error {
	envsDir := getEnvironmentDir()
	entries, err := os.ReadDir(envsDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				if flagDebug {
					loggerErr.Println(err)
				}
				continue
			}
			if time.Since(info.ModTime()) > StaleEnvironmentTime {
				staleEnvAbsPath := path.Join(envsDir, entry.Name())
				if !isEnvLocked(staleEnvAbsPath) {
					_, err := findProcessWithPrefix(staleEnvAbsPath)
					if err == ErrNoProcessFound {
						if flagDebug {
							loggerErr.Printf("Removing stale virtual environment %s...\n", staleEnvAbsPath)
						}
						err = removeDir(staleEnvAbsPath)
						if err != nil {
							if flagDebug {
								loggerErr.Println(err)
							}
						}
					}
				}
			}
		}
	}
	return err
}

func getEnvironmentDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return path.Join("/tmp/", EnvironmentsDir)
	}
	return path.Join(homeDir, EnvironmentsDir)
}
