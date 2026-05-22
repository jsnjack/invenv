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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-cmd/cmd"
	"github.com/mattheath/base62"
)

// EnvironmentsDirName is the directory name under os.UserCacheDir() where
// virtual environments are kept. Resolves to ~/.cache/invenv on Linux,
// ~/Library/Caches/invenv on macOS, etc. — the platform's cache directory
// per the XDG Base Directory Specification.
const EnvironmentsDirName = "invenv"

const CyanColor = "\033[1;36m"
const ResetColor = "\033[0m"

// LockStaleTime is the maximum lockfile mtime age before the lock is
// considered stale. Heartbeats from a healthy lock owner refresh the mtime
// every heartbeatInterval, so a healthy lock's mtime is always recent;
// an mtime older than LockStaleTime means the owner is gone (or its PID
// was reused — see isLockStale). Declared as a var so tests can shorten it.
var LockStaleTime = 15 * time.Minute

// lockCheckInterval is the poll period inside waitUntilEnvIsUnlocked.
// Declared as a var (not const) so tests can shorten it.
var lockCheckInterval = 1 * time.Second

// heartbeatInterval is how often a lock owner refreshes the lockfile's
// mtime so concurrent invenv processes see the lock as healthy. Must stay
// well below LockStaleTime. Declared as a var so tests can shorten it.
var heartbeatInterval = 5 * time.Minute

// StaleEnvironmentTime is the time after which the virtual environment is considered stale
const StaleEnvironmentTime = 14 * 24 * time.Hour

// errStaleLockfile is returned when the lockfile is stale - older than LockStaleTime
var errStaleLockfile = fmt.Errorf("stale lockfile")

// getFileHash returns the first 8 hex chars of the file's SHA1 digest.
// (Used as part of the venv ID — collision space is 2^32 per user, which
// is acceptable given the scope.)
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

// lockInfo is the parsed content of a lock file.
type lockInfo struct {
	pid     int
	startNs int64
}

// readLockInfo parses the lock file for envDir. Returns os.ErrNotExist if
// missing. Returns a zero lockInfo (with nil error) if the file is empty
// or malformed — this case covers lockfiles written by an older invenv
// version that crashed.
func readLockInfo(envDir string) (lockInfo, error) {
	data, err := os.ReadFile(generateLockFileName(envDir))
	if err != nil {
		return lockInfo{}, err
	}
	var info lockInfo
	n, _ := fmt.Sscanf(string(data), "%d %d", &info.pid, &info.startNs)
	if n != 2 {
		return lockInfo{}, nil
	}
	return info, nil
}

// isPidAlive reports whether a process with the given pid currently exists.
// Cross-platform: sends signal 0, the POSIX existence probe. Returns false
// for pid <= 0 and treats EPERM as "alive" (process exists but is owned by
// another user, so we cannot signal it).
func isPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// isLockStale reports whether the lockfile for envDir is stale and should
// be cleared. A lock is stale when:
//   - the file is empty or malformed (older invenv version that crashed);
//   - the named owner PID is dead;
//   - the owner PID is alive but the lockfile mtime is older than
//     LockStaleTime — heartbeats keep mtime fresh during a real build, so
//     an old mtime means the alive PID is probably a reused one.
//
// Returns (false, nil) when no lockfile exists.
func isLockStale(envDir string) (bool, error) {
	info, err := readLockInfo(envDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read lock info: %w", err)
	}
	if info.pid == 0 {
		return true, nil
	}
	if !isPidAlive(info.pid) {
		return true, nil
	}
	stat, err := os.Stat(generateLockFileName(envDir))
	if err == nil && time.Since(stat.ModTime()) > LockStaleTime {
		return true, nil
	}
	return false, nil
}

// heartbeat tracks the goroutine refreshing a lockfile's mtime.
type heartbeat struct {
	stop chan struct{}
	done chan struct{}
}

// heartbeats indexes active heartbeats by envDir.
var heartbeats sync.Map

// startHeartbeat launches a goroutine that bumps the lockfile's mtime on
// every heartbeatInterval. The goroutine exits when stopHeartbeat is
// called for the same envDir.
func startHeartbeat(envDir string) {
	hb := &heartbeat{stop: make(chan struct{}), done: make(chan struct{})}
	heartbeats.Store(envDir, hb)
	go func() {
		defer close(hb.done)
		heartbeatLoop(envDir, hb.stop)
	}()
}

// stopHeartbeat signals the heartbeat goroutine for envDir to exit and
// waits for it to finish. Safe to call when no heartbeat is registered.
func stopHeartbeat(envDir string) {
	v, ok := heartbeats.LoadAndDelete(envDir)
	if !ok {
		return
	}
	hb := v.(*heartbeat)
	close(hb.stop)
	<-hb.done
}

func heartbeatLoop(envDir string, stop chan struct{}) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	lockPath := generateLockFileName(envDir)
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			now := time.Now()
			if err := os.Chtimes(lockPath, now, now); err != nil {
				trace("heartbeat chtimes", "err", err)
			}
		}
	}
}

// lockEnv acquires the lock for envDir. The lockfile contains the
// acquiring process's PID and a nanosecond timestamp, written atomically
// via os.Link from a temp file so readers never observe an empty in-flight
// file. A heartbeat goroutine is started to refresh the lockfile mtime
// for the duration of the lock.
func lockEnv(envDir string) error {
	slog.Debug("locking virtual environment", "dir", envDir)
	lockPath := generateLockFileName(envDir)
	if err := os.MkdirAll(path.Dir(lockPath), 0755); err != nil {
		return fmt.Errorf("mkdir lock parent: %w", err)
	}

	content := fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().UnixNano())
	tmpPath := fmt.Sprintf("%s.tmp.%d.%d", lockPath, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write temp lockfile: %w", err)
	}
	defer func() {
		if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			trace("remove temp lockfile", "err", err)
		}
	}()

	if err := os.Link(tmpPath, lockPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrEnvAlreadyLocked
		}
		return fmt.Errorf("link lockfile: %w", err)
	}
	startHeartbeat(envDir)
	return nil
}

// unlockEnv releases the lock for envDir. Stops the heartbeat goroutine
// before removing the file so a concurrent reader that catches the file
// mid-removal can't be confused by a still-refreshing mtime.
func unlockEnv(envDir string) error {
	slog.Debug("unlocking virtual environment", "dir", envDir)
	stopHeartbeat(envDir)
	err := os.Remove(generateLockFileName(envDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove lockfile: %w", err)
	}
	return nil
}

// waitUntilEnvIsUnlocked blocks until the lock for envDir is released.
// Returns errStaleLockfile when the lock is determined to be stale
// (see isLockStale) so callers can recover and rebuild the environment.
func waitUntilEnvIsUnlocked(envDir string) error {
	slog.Debug("waiting for lock on virtual environment", "dir", envDir)
	for {
		if !isEnvLocked(envDir) {
			slog.Debug("lock released", "dir", envDir)
			return nil
		}
		stale, err := isLockStale(envDir)
		if err != nil {
			return fmt.Errorf("check lock staleness: %w", err)
		}
		if stale {
			return errStaleLockfile
		}
		time.Sleep(lockCheckInterval)
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

// getEnvironmentDir returns the directory where virtual environments are
// stored. Follows the XDG Base Directory Specification via os.UserCacheDir().
//
// Note: this is a behavior change from invenv 0.x, which used
// ~/.local/invenv. Envs cached at the old location are no longer
// consulted; delete them manually with `rm -rf ~/.local/invenv`.
func getEnvironmentDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return path.Join("/tmp", EnvironmentsDirName)
	}
	return path.Join(cacheDir, EnvironmentsDirName)
}
