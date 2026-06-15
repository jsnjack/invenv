package cmd

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// warningWriter is where user-facing warnings (e.g. interpreter fallback)
// are written. A package var so tests can capture output.
var warningWriter io.Writer = os.Stderr

// resolvePythonInterpreter picks the python interpreter to use:
//
//   - if override is non-empty (-p flag), it is returned verbatim — and is
//     an error if it isn't in $PATH.
//   - else, if scriptPath is non-empty and its shebang names an interpreter
//     that exists in $PATH, that interpreter is returned.
//   - else, "python" is returned. If a specific interpreter was requested
//     via shebang but not found, a one-line warning is written to
//     warningWriter so the substitution is visible.
//
// On success the returned interpreter is guaranteed to be in $PATH.
func resolvePythonInterpreter(scriptPath, override string) (string, error) {
	if override != "" {
		if _, err := exec.LookPath(override); err != nil {
			return "", fmt.Errorf("find python interpreter %s: %w", override, err)
		}
		return override, nil
	}

	requested := ""
	if scriptPath != "" {
		shebang, err := extractPythonFromShebang(scriptPath)
		if err != nil {
			slog.Debug("extract python from shebang", "err", err)
		}
		requested = shebang
	}
	if requested == "" {
		requested = "python"
	}

	if _, err := exec.LookPath(requested); err == nil {
		return requested, nil
	}

	// Requested interpreter is missing. Fall back to "python" and surface
	// the substitution as a warning if the user actually specified one.
	if requested != "python" {
		if _, werr := fmt.Fprintf(warningWriter,
			"\nwarning: %q (from shebang) is not in $PATH; falling back to \"python\"\n",
			requested); werr != nil {
			slog.Debug("write fallback warning", "err", werr)
		}
	}
	if _, err := exec.LookPath("python"); err != nil {
		return "", fmt.Errorf("find python interpreter python: %w", err)
	}
	return "python", nil
}

const VEnvInfoFilename = ".venv.version"
const VEnvDirDefaultName = ".venv"

// VEnvBuiltMarker is the filename written into an environment directory
// after a successful CreateEnv + InstallRequirementsInEnv. Its presence is
// the integrity signal: a directory that exists without this marker is
// treated as a partial build and rebuilt.
const VEnvBuiltMarker = ".invenv-built"

// envHealth describes whether an environment directory is usable.
type envHealth int

const (
	envMissing envHealth = iota
	envBroken
	envHealthy
)

// checkEnvHealth inspects the directory at envDir.
//
//   - envMissing: the directory does not exist.
//   - envBroken:  the directory exists but is unreadable, or appears to be a
//     partial build (no marker, no bin/python).
//   - envHealthy: the directory exists with the built marker — or has a
//     bin/python from an older invenv version, in which case the marker is
//     written now (best-effort migration so future checks are fast).
func checkEnvHealth(envDir string) envHealth {
	_, err := os.Stat(envDir)
	if errors.Is(err, os.ErrNotExist) {
		return envMissing
	}
	if err != nil {
		// Permission, I/O, etc. Treat conservatively as broken so we rebuild.
		slog.Debug("stat envdir", "dir", envDir, "err", err)
		return envBroken
	}

	markerPath := filepath.Join(envDir, VEnvBuiltMarker)
	if _, err := os.Stat(markerPath); err == nil {
		return envHealthy
	}

	// No marker. Two cases:
	//   (a) Legacy env from a pre-marker invenv version (still functional).
	//   (b) Partial build from a crashed run.
	// Treat presence of bin/python as the legacy "is this env functional"
	// signal — that's the contract older invenv versions relied on.
	if _, err := os.Stat(filepath.Join(envDir, "bin", "python")); err == nil {
		if err := os.WriteFile(markerPath, nil, 0644); err != nil {
			trace("migrate legacy env marker", "dir", envDir, "err", err)
		}
		return envHealthy
	}
	return envBroken
}

// Script represents a Python script
type Script struct {
	AbsolutePath      string // Full path to the script
	EnvDir            string // Full path to the virtual environment
	PythonInterpreter string // Python interpreter to use
	RequirementsPath  string // Full path to the requirements file
	venvID            string // Unique identifier for the virtual environment
	fromInitCommand   bool   // True if the script was created with init subcommand
}

// touchEnvOnUse bumps envDir's mtime so clearStaleEnvs sees an active env
// as not stale. Without this, a daily script whose env never changes would
// be deleted after StaleEnvironmentTime — directory mtime only updates on
// structural changes, not on reads or execs of files inside.
//
// Best-effort: a chtimes failure (e.g. read-only mount) is logged at debug
// and the run continues normally.
func touchEnvOnUse(envDir string) {
	now := time.Now()
	if err := os.Chtimes(envDir, now, now); err != nil {
		slog.Debug("touch env on use", "dir", envDir, "err", err)
	}
}

// EnsureEnv ensures that the virtual environment for the script exists. It creates
// a new virtual environment or waits until it is created by another process.
//
// On a successful return, EnsureEnv touches the env directory's mtime so
// clearStaleEnvs treats it as recently used.
func (s *Script) EnsureEnv(deleteOldEnv bool) (err error) {
	defer func() {
		if err == nil {
			touchEnvOnUse(s.EnvDir)
		}
	}()

	// forceRebuild means the env must be (re)built even if the directory
	// currently looks healthy: the caller asked (-n), the requirements
	// changed (init id mismatch), or a previous owner crashed mid-build
	// (stale lock). A merely-broken/missing observation does NOT force a
	// rebuild — it only sends us down the build path, where the verdict is
	// re-checked under the lock (see buildLockedEnv) so we don't tear down
	// an env a process we waited on just finished building.
	forceRebuild := deleteOldEnv
	readOperationOnly := !deleteOldEnv

	switch checkEnvHealth(s.EnvDir) {
	case envMissing:
		readOperationOnly = false
	case envBroken:
		slog.Debug("environment exists but is broken; rebuilding", "dir", s.EnvDir)
		readOperationOnly = false
	case envHealthy:
		// Use as-is unless the caller explicitly asked for a rebuild.
	}

	if s.fromInitCommand && readOperationOnly {
		// If the script was created with init command, it doesn't have a unique
		// environment ID as part of its path, so we can't rely on the presence of
		// the environment directory to determine if it exists.
		infoFilename := path.Join(s.EnvDir, VEnvInfoFilename)
		data, rerr := os.ReadFile(infoFilename)
		if rerr != nil {
			readOperationOnly = false
			slog.Debug("read environment info file", "err", rerr)
		} else if strings.TrimSpace(string(data)) != s.venvID {
			// Environment ID mismatch, recreate the environment
			readOperationOnly = false
			forceRebuild = true
			slog.Debug("environment id mismatch", "got", string(data), "want", s.venvID)
		}
	}

	err = waitUntilEnvIsUnlocked(s.EnvDir)
	switch {
	case err == nil:
		break
	case errors.Is(err, errStaleLockfile):
		slog.Debug("recreating environment", "reason", err)
		// Lockfile is stale (owner crashed or otherwise abandoned it).
		// Clear it so the lockEnv call below can acquire it. clearStaleLock
		// re-verifies staleness so we don't remove a fresh lock created by
		// another waiter that recovered first.
		if uerr := clearStaleLock(s.EnvDir); uerr != nil {
			return fmt.Errorf("clear stale lockfile: %w", uerr)
		}
		readOperationOnly = false
		forceRebuild = true
	default:
		return fmt.Errorf("wait for environment unlock: %w", err)
	}

	if !readOperationOnly {
		err = lockEnv(s.EnvDir)
		switch {
		case errors.Is(err, ErrEnvAlreadyLocked):
			// Another process acquired the lock between our check and our
			// lock attempt. Wait for it to finish; we then fall out of this
			// block to the shared health check below, which verifies what
			// the other process produced.
			if waitErr := waitUntilEnvIsUnlocked(s.EnvDir); waitErr != nil {
				return fmt.Errorf("wait for environment unlock after contention: %w", waitErr)
			}
		case err != nil:
			return fmt.Errorf("lock environment: %w", err)
		default:
			// We hold the lock; build (or reuse) and release it, all inside.
			return s.buildLockedEnv(forceRebuild)
		}
	}

	// Reached for pure read-only use, or after waiting out a process that
	// held the lock. The health verdict at the top of this function predates
	// that wait, so re-verify before reporting success: a concurrent rebuild
	// that failed (and removed the env) must not be reported as ready, or
	// the caller would exec a missing interpreter / print a path to a
	// vanished env.
	if checkEnvHealth(s.EnvDir) != envHealthy {
		return fmt.Errorf("environment %s was built concurrently by another process which failed; re-run to rebuild", s.EnvDir)
	}
	return nil
}

// buildLockedEnv (re)builds the environment and installs its requirements.
// The caller must already hold the environment lock; buildLockedEnv always
// releases it before returning — via unlockEnv on success (and on the
// healthy-reuse shortcut), or via RemoveEnv when a build step fails.
//
// forceRebuild is true when a rebuild is required even if the directory
// currently looks healthy (explicit -n, requirements changed, or stale-lock
// recovery). When it is false, the now-serialized health check can short
// out: a process we waited on may have just produced a healthy env, and we
// must not tear down a fresh build to recreate an identical one.
func (s *Script) buildLockedEnv(forceRebuild bool) error {
	// Re-check health now that the build is serialized under our lock. For
	// hash-keyed envs a healthy directory is by definition the right env, so
	// reuse it. (init envs live at a fixed path where "healthy" doesn't imply
	// "matching requirements", so they always rebuild when flagged.)
	if !forceRebuild && !s.fromInitCommand && checkEnvHealth(s.EnvDir) == envHealthy {
		slog.Debug("environment became healthy while waiting; reusing", "dir", s.EnvDir)
		if err := unlockEnv(s.EnvDir); err != nil {
			return fmt.Errorf("unlock environment: %w", err)
		}
		return nil
	}

	// Clear any existing directory (a no-op when missing) so a partial or
	// outdated build cannot leak into the new env. Not s.RemoveEnv(), which
	// would also release the lock we still need.
	if err := removeDir(s.EnvDir); err != nil {
		// Release the lock we hold so the next run can recover immediately
		// instead of waiting for stale-lock detection.
		if uerr := unlockEnv(s.EnvDir); uerr != nil {
			slog.Debug("unlock after failed removeDir", "dir", s.EnvDir, "err", uerr)
		}
		return fmt.Errorf("remove old environment: %w", err)
	}

	if err := s.CreateEnv(); err != nil {
		// If the build failed, remove the environment so we don't leave a
		// broken environment behind and other scripts won't use it.
		if removeErr := s.RemoveEnv(); removeErr != nil {
			return errors.Join(err, fmt.Errorf("remove broken environment: %w", removeErr))
		}
		return err
	}
	if err := s.InstallRequirementsInEnv(); err != nil {
		if removeErr := s.RemoveEnv(); removeErr != nil {
			return errors.Join(err, fmt.Errorf("remove broken environment: %w", removeErr))
		}
		return err
	}

	// Write the integrity marker. Best-effort: a failure here leaves the
	// env in the same state an older invenv would have produced, and
	// checkEnvHealth will migrate it on the next run.
	markerPath := filepath.Join(s.EnvDir, VEnvBuiltMarker)
	if err := os.WriteFile(markerPath, nil, 0644); err != nil {
		slog.Debug("write built marker", "path", markerPath, "err", err)
	}

	if s.fromInitCommand {
		// Write the environment ID to the info file
		infoFilename := path.Join(s.EnvDir, VEnvInfoFilename)
		if err := os.WriteFile(infoFilename, []byte(s.venvID), 0644); err != nil {
			// Roll back the partial build so the lock is released and
			// the next run rebuilds cleanly. Without this, the lock
			// would be leaked and only recovered via stale-detection.
			wrapped := fmt.Errorf("write environment info file: %w", err)
			if removeErr := s.RemoveEnv(); removeErr != nil {
				return errors.Join(wrapped, fmt.Errorf("remove broken environment: %w", removeErr))
			}
			return wrapped
		}
		slog.Debug("wrote environment id", "path", infoFilename)
	}

	// If all operations succeeded, unlock the environment
	if err := unlockEnv(s.EnvDir); err != nil {
		return fmt.Errorf("unlock environment: %w", err)
	}
	return nil
}

// CreateEnv creates a virtual environment for the script
func (s *Script) CreateEnv() error {
	var err error
	var output []string

	slog.Debug("creating new virtual environment")

	// First, try to use venv module
	err = exec.Command(s.PythonInterpreter, "-m", "venv", "--help").Run()
	if err == nil {
		slog.Debug("using venv module")
		if flagDebug {
			err = execCmd(s.PythonInterpreter, "-m", "venv", s.EnvDir)
		} else {
			output, err = execCmdSilent(s.PythonInterpreter, "-m", "venv", s.EnvDir)
		}
	} else {
		// Ensure virtualenv is installed
		var virtualenvPath string
		virtualenvPath, err = exec.LookPath("virtualenv")
		if err != nil {
			return fmt.Errorf("find virtualenv: %w", err)
		}
		slog.Debug("using virtualenv", "path", virtualenvPath)
		if flagDebug {
			err = execCmd(virtualenvPath, "--python", s.PythonInterpreter, s.EnvDir)
		} else {
			output, err = execCmdSilent(virtualenvPath, "--python", s.PythonInterpreter, s.EnvDir)
		}
	}
	if err != nil {
		// Print buffered combined output if the command failed
		if !flagDebug {
			fmt.Fprintln(os.Stderr, "\n", strings.Join(output, "\n"))
		}
		return fmt.Errorf("create virtual environment: %w", err)
	}
	return nil
}

// InstallRequirementsInEnv installs the requirements file (if any) into the
// virtual environment using pip.
func (s *Script) InstallRequirementsInEnv() error {
	var err error
	var output []string

	if s.RequirementsPath == "" {
		return nil
	}

	if flagDebug {
		err = execCmd(path.Join(s.EnvDir, "bin/pip"), "install", "--no-input", "-r", s.RequirementsPath)
	} else {
		output, err = execCmdSilent(path.Join(s.EnvDir, "bin/pip"), "install", "--no-input", "-r", s.RequirementsPath)
	}
	if err != nil {
		// Print buffered combined output if the command failed
		if !flagDebug {
			fmt.Fprintln(os.Stderr, "\n", strings.Join(output, "\n"))
		}
		return fmt.Errorf("install requirements: %w", err)
	}
	return nil
}

// RemoveEnv removes the virtual environment for the script. Also removes the lockfile.
// The lockfile is only removed if the directory removal succeeds, so that a broken
// environment is detected and recreated on the next run.
func (s *Script) RemoveEnv() error {
	slog.Debug("deleting virtual environment", "dir", s.EnvDir)

	// Remove the virtual environment directory
	if err := removeDir(s.EnvDir); err != nil {
		// Do not unlock the environment if the directory removal failed.
		// This ensures that the next run detects the stale lock and
		// recreates the environment instead of using a broken one.
		return fmt.Errorf("remove environment dir: %w", err)
	}
	if err := unlockEnv(s.EnvDir); err != nil {
		return fmt.Errorf("unlock environment: %w", err)
	}
	return nil
}

// resolveEnvIdentity computes the pieces that identify a virtual
// environment: the requirements file (if any), the interpreter, and the
// env ID derived from the requirements hash and the python version.
//
// requirementsProbePath anchors the requirements-file name guessing (the
// file's directory is searched). shebangSource is the script whose shebang
// may name the interpreter; empty for `init`, which has no script and
// falls back to "python".
func resolveEnvIdentity(requirementsProbePath, shebangSource, interpreterOverride, requirementsOverride string) (requirementsFile, pythonInterpreter, envID string, err error) {
	requirementsFile, err = getRequirementsFileForScript(requirementsProbePath, requirementsOverride)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve requirements file: %w", err)
	}

	if requirementsFile == "" {
		slog.Debug("no requirements file found")
	} else {
		slog.Debug("found requirements file", "path", requirementsFile)
	}

	requirementsHash := ""
	if requirementsFile != "" {
		requirementsHash, err = getFileHash(requirementsFile)
		if err != nil {
			return "", "", "", fmt.Errorf("hash requirements file: %w", err)
		}
	}

	slog.Debug("requirements hash", "hash", requirementsHash)

	pythonInterpreter, err = resolvePythonInterpreter(shebangSource, interpreterOverride)
	if err != nil {
		return "", "", "", err
	}

	pythonVersion, err := getPythonVersion(pythonInterpreter)
	if err != nil {
		return "", "", "", fmt.Errorf("get python version: %w", err)
	}

	slog.Debug("using python interpreter", "version", pythonVersion)

	envID = generateEnvID(requirementsHash, pythonVersion)
	slog.Debug("generated environment id", "id", envID)

	return requirementsFile, pythonInterpreter, envID, nil
}

// NewScript creates a new Script instance
func NewScript(scriptName string, interpreterOverride string, requirementsOverride string) (*Script, error) {
	scriptPath, err := filepath.Abs(scriptName)
	if err != nil {
		return nil, fmt.Errorf("resolve script path: %w", err)
	}

	// Check if the script exists
	if _, err = os.Stat(scriptPath); err != nil {
		return nil, fmt.Errorf("stat script: %w", err)
	}

	requirementsFile, pythonInterpreter, envID, err := resolveEnvIdentity(scriptPath, scriptPath, interpreterOverride, requirementsOverride)
	if err != nil {
		return nil, err
	}

	envDir := path.Join(getEnvironmentDir(), envID+".env")

	slog.Debug("using virtual environment", "dir", envDir)

	script := &Script{
		AbsolutePath:      scriptPath,
		EnvDir:            envDir,
		PythonInterpreter: pythonInterpreter,
		RequirementsPath:  requirementsFile,
		venvID:            envID,
	}
	return script, nil
}

// NewInitCmd creates a new Script instance for the `init` subcommand. The
// venv is placed at .venv inside the current working directory.
func NewInitCmd(interpreterOverride string, requirementsOverride string) (*Script, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}

	// The probe path anchors requirements guessing to cwd; init has no
	// script, so only the plain requirements.txt pattern can match.
	requirementsFile, pythonInterpreter, envID, err := resolveEnvIdentity(path.Join(cwd, ".placeholder"), "", interpreterOverride, requirementsOverride)
	if err != nil {
		return nil, err
	}

	envDir := path.Join(cwd, VEnvDirDefaultName)

	slog.Debug("using virtual environment", "dir", envDir)

	script := &Script{
		AbsolutePath:      cwd,
		EnvDir:            envDir,
		PythonInterpreter: pythonInterpreter,
		RequirementsPath:  requirementsFile,
		venvID:            envID,
		fromInitCommand:   true,
	}
	return script, nil
}
