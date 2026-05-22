package cmd

import (
	"bufio"
	"crypto/sha1"
	"errors"
	"fmt"
	"log/slog"
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

// LockStaleTime is the time after which the lock is considered stale.
// Declared as a var (not const) so tests can shorten it.
var LockStaleTime = 15 * time.Minute

// lockCheckInterval is the poll period inside waitUntilEnvIsUnlocked.
// Declared as a var (not const) so tests can shorten it.
var lockCheckInterval = 1 * time.Second

// StaleEnvironmentTime is the time after which the virtual environment is considered stale
const StaleEnvironmentTime = 14 * 24 * time.Hour

// errStaleLockfile is returned when the lockfile is stale - older than LockStaleTime
var errStaleLockfile = fmt.Errorf("stale lockfile")

// getFileHash calculates the SHA256 hash of the file
func getFileHash(filename string) (string, error) {
	// Check that the file exists
	if _, err := os.Stat(filename); err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}

	dataBytes, err := os.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
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

// ErrEnvAlreadyLocked is returned when the environment is already locked by
// another process
var ErrEnvAlreadyLocked = fmt.Errorf("environment is already locked")

func lockEnv(envDir string) error {
	slog.Debug("locking virtual environment", "dir", envDir)
	lockFileName := generateLockFileName(envDir)
	if err := os.MkdirAll(path.Dir(lockFileName), 0755); err != nil {
		return fmt.Errorf("mkdir lock parent: %w", err)
	}
	// Use O_CREATE|O_EXCL to atomically create the lock file. This ensures
	// that only one process can acquire the lock. If the file already exists,
	// another process holds the lock.
	f, err := os.OpenFile(lockFileName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			return ErrEnvAlreadyLocked
		}
		return fmt.Errorf("create lockfile: %w", err)
	}
	if err := f.Close(); err != nil {
		trace("close lockfile", "err", err)
	}
	return nil
}

func unlockEnv(envDir string) error {
	slog.Debug("unlocking virtual environment", "dir", envDir)
	lockFileName := generateLockFileName(envDir)
	err := os.Remove(lockFileName)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove lockfile: %w", err)
	}
	return nil
}

func waitUntilEnvIsUnlocked(envDir string) error {
	slog.Debug("acquiring lock on virtual environment", "dir", envDir)
	defer slog.Debug("lock acquired", "dir", envDir)
	now := time.Now()
	for {
		if !isEnvLocked(envDir) {
			return nil
		}
		time.Sleep(lockCheckInterval)
		if time.Since(now) > LockStaleTime {
			return errStaleLockfile
		}
		// Lockfile is not stale but lets check if there is a process which uses this virtual environment
		if runtime.GOOS == "linux" {
			_, err := findProcessWithPrefix(envDir)
			if errors.Is(err, ErrNoProcessFound) {
				return err
			}
		}
	}
}

// extractPythonFromShebang extracts the interpreter path from a shebang
func extractPythonFromShebang(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			trace("close file", "err", err)
		}
	}()

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
		return "", fmt.Errorf("scan file: %w", err)
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
				fmt.Fprintln(os.Stderr, line)
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

// printProgress prints a progress message. In non-debug mode it overwrites the
// current line on stderr so the user sees a single rolling status. In debug
// mode the message is emitted as a debug log so it lands wherever logs are
// routed (stderr or the trace file).
func printProgress(s string) {
	if flagDebug || flagTrace {
		slog.Debug(s)
		return
	}
	if flagSilent {
		return
	}
	// Clear the line
	fmt.Fprint(os.Stderr, "\033[2K\r")
	fmt.Fprint(os.Stderr, CyanColor+s+ResetColor)
}

func removeDir(dir string) error {
	err := os.RemoveAll(dir)
	if err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			// Extreme case, try with sudo
			if sudoErr := execCmd("sudo", "rm", "-rf", dir); sudoErr != nil {
				return fmt.Errorf("delete directory with sudo: %w", sudoErr)
			}
			return nil
		}
		return fmt.Errorf("delete directory: %w", err)
	}
	return nil
}

func getPythonVersion(pythonInterpreter string) (string, error) {
	// Verify that the Python version used to create the virtual environment is the same
	// as the current Python version
	currentPythonVersion, err := exec.Command(pythonInterpreter, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("get Python version: %w", err)
	}
	currentPythonVersionStr := strings.TrimSpace(string(currentPythonVersion))
	slog.Debug("python interpreter version", "interpreter", pythonInterpreter, "version", currentPythonVersionStr)
	return currentPythonVersionStr, nil
}

// getRequirementsFileForScript returns the requirements file for the script
func getRequirementsFileForScript(scriptPath string, requirementsOverride string) (string, error) {
	scriptPath, err := filepath.Abs(scriptPath)
	if err != nil {
		return "", fmt.Errorf("resolve absolute script path: %w", err)
	}

	// Select requirements file. First check if the file provided in overrides exists
	if requirementsOverride != "" {
		if !path.IsAbs(requirementsOverride) {
			cwd, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("get working directory: %w", err)
			}
			return path.Join(cwd, requirementsOverride), nil
		}
		return requirementsOverride, nil
	}

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
		slog.Debug("checking candidate requirements file", "path", possibleRequirementsFile)
		_, err := os.Stat(possibleRequirementsFile)
		if err == nil {
			return possibleRequirementsFile, nil
		}
		slog.Debug("candidate not found", "err", err)
	}
	return "", nil
}

// clearStaleEnvs removes stale virtual environments
func clearStaleEnvs() error {
	envsDir := getEnvironmentDir()
	entries, err := os.ReadDir(envsDir)
	if err != nil {
		return fmt.Errorf("read environments dir: %w", err)
	}

	for _, entry := range entries {
		if err := processStaleEntry(envsDir, entry); err != nil {
			slog.Debug("process stale entry", "name", entry.Name(), "err", err)
		}
	}
	return nil
}

func processStaleEntry(envsDir string, entry os.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("entry info: %w", err)
	}

	if time.Since(info.ModTime()) <= StaleEnvironmentTime {
		return nil
	}

	absPath := path.Join(envsDir, entry.Name())

	if entry.IsDir() {
		return cleanupStaleEnv(absPath)
	}

	if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".lock") {
		return cleanupDanglingLockfile(envsDir, entry.Name())
	}

	return nil
}

// cleanupStaleEnv removes a stale virtual environment if no process is using it
func cleanupStaleEnv(envPath string) error {
	_, err := findProcessWithPrefix(envPath)
	if err == nil {
		slog.Debug("virtual environment still in use, skipping", "path", envPath)
		return nil
	}
	if !errors.Is(err, ErrNoProcessFound) {
		return fmt.Errorf("check process for %s: %w", envPath, err)
	}

	slog.Debug("removing stale virtual environment", "path", envPath)

	// Best effort cleanup
	if err := unlockEnv(envPath); err != nil {
		trace("unlock stale env", "path", envPath, "err", err)
	}
	return removeDir(envPath)
}

// cleanupDanglingLockfile removes a lockfile if there is no corresponding environment
func cleanupDanglingLockfile(envsDir, lockName string) error {
	envName := strings.TrimSuffix(lockName, ".lock")
	envPath := path.Join(envsDir, envName)

	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		return nil // Environment exists
	}

	lockPath := path.Join(envsDir, lockName)
	slog.Debug("removing stale lockfile", "path", lockPath)
	if err := os.Remove(lockPath); err != nil {
		return fmt.Errorf("remove lockfile: %w", err)
	}
	return nil
}

func getEnvironmentDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return path.Join("/tmp/", EnvironmentsDir)
	}
	return path.Join(homeDir, EnvironmentsDir)
}
