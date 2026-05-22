package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

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

// EnsureEnv ensures that the virtual environment for the script exists. It creates
// a new virtual environment or waits until it is created by another process
func (s *Script) EnsureEnv(deleteOldEnv bool) error {
	readOperationOnly := !deleteOldEnv

	switch checkEnvHealth(s.EnvDir) {
	case envMissing:
		readOperationOnly = false
	case envBroken:
		slog.Debug("environment exists but is broken; rebuilding", "dir", s.EnvDir)
		readOperationOnly = false
		deleteOldEnv = true
	case envHealthy:
		// Use as-is unless the caller explicitly asked for a rebuild.
	}

	if s.fromInitCommand && readOperationOnly {
		// If the script was created with init command, it doesn't have a unique
		// environment ID as part of its path, so we can't rely on the presence of
		// the environment directory to determine if it exists.
		infoFilename := path.Join(s.EnvDir, VEnvInfoFilename)
		data, err := os.ReadFile(infoFilename)
		if err != nil {
			readOperationOnly = false
			slog.Debug("read environment info file", "err", err)
		} else {
			if strings.TrimSpace(string(data)) != s.venvID {
				// Environment ID mismatch, recreate the environment
				readOperationOnly = false
				deleteOldEnv = true
				slog.Debug("environment id mismatch", "got", string(data), "want", s.venvID)
			}
		}
	}

	err := waitUntilEnvIsUnlocked(s.EnvDir)
	switch {
	case err == nil:
		break
	case errors.Is(err, errStaleLockfile):
		slog.Debug("recreating environment", "reason", err)
		// Lockfile is stale (owner crashed or otherwise abandoned it).
		// Clear it so the lockEnv call below can acquire it.
		if uerr := unlockEnv(s.EnvDir); uerr != nil {
			return fmt.Errorf("clear stale lockfile: %w", uerr)
		}
		readOperationOnly = false
		deleteOldEnv = true
	default:
		return fmt.Errorf("wait for environment unlock: %w", err)
	}

	if !readOperationOnly {
		err = lockEnv(s.EnvDir)
		if errors.Is(err, ErrEnvAlreadyLocked) {
			// Another process acquired the lock between our check and our
			// lock attempt. Wait for it to finish and use the environment.
			if waitErr := waitUntilEnvIsUnlocked(s.EnvDir); waitErr != nil {
				return fmt.Errorf("wait for environment unlock after contention: %w", waitErr)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock environment: %w", err)
		}

		if deleteOldEnv {
			// Do not use s.RemoveEnv() here because it unlocks the environment
			if err := removeDir(s.EnvDir); err != nil {
				return fmt.Errorf("remove old environment: %w", err)
			}
		}

		if err := s.CreateEnv(); err != nil {
			// If the installation failed, remove the environment so we don't
			// leave a broken environment behind and other scripts won't use it
			if removeErr := s.RemoveEnv(); removeErr != nil {
				return errors.Join(err, fmt.Errorf("remove broken environment: %w", removeErr))
			}
			return err
		}
		if err := s.InstallRequirementsInEnv(); err != nil {
			// If the installation failed, remove the environment so we don't
			// leave a broken environment behind and other scripts won't use it
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
				return fmt.Errorf("write environment info file: %w", err)
			}
			slog.Debug("wrote environment id", "path", infoFilename)
		}

		// If all operations succeeded, unlock the environment
		if err := unlockEnv(s.EnvDir); err != nil {
			return fmt.Errorf("unlock environment: %w", err)
		}
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

	// Try to find requirements.txt file for the script
	requirementsFile, err := getRequirementsFileForScript(scriptPath, requirementsOverride)
	if err != nil {
		return nil, fmt.Errorf("resolve requirements file: %w", err)
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
			return nil, fmt.Errorf("hash requirements file: %w", err)
		}
	}

	slog.Debug("requirements hash", "hash", requirementsHash)

	var pythonInterpreter string
	if interpreterOverride == "" {
		pythonInterpreter, err = extractPythonFromShebang(scriptPath)
		if err != nil {
			slog.Debug("extract python from shebang", "err", err)
		}
		if pythonInterpreter == "" {
			pythonInterpreter = "python"
		}
	} else {
		pythonInterpreter = interpreterOverride
	}

	// Check if the python interpreter exists in path
	_, err = exec.LookPath(pythonInterpreter)
	if err != nil && interpreterOverride != "" {
		return nil, fmt.Errorf("find python interpreter %s: %w", pythonInterpreter, err)
	} else if err != nil {
		slog.Debug("python interpreter not found, falling back", "interpreter", pythonInterpreter, "err", err)
		pythonInterpreter = "python"
		_, err = exec.LookPath(pythonInterpreter)
		if err != nil {
			return nil, fmt.Errorf("find python interpreter %s: %w", pythonInterpreter, err)
		}
	}

	pythonVersion, err := getPythonVersion(pythonInterpreter)
	if err != nil {
		return nil, fmt.Errorf("get python version: %w", err)
	}

	slog.Debug("using python interpreter", "version", pythonVersion)

	envID := generateEnvID(requirementsHash, pythonVersion)

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

	// Try to find requirements.txt file for the script
	requirementsFile, err := getRequirementsFileForScript(path.Join(cwd, ".placeholder"), requirementsOverride)
	if err != nil {
		return nil, fmt.Errorf("resolve requirements file: %w", err)
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
			return nil, fmt.Errorf("hash requirements file: %w", err)
		}
	}

	slog.Debug("requirements hash", "hash", requirementsHash)

	var pythonInterpreter string
	if interpreterOverride == "" {
		pythonInterpreter = "python"
	} else {
		pythonInterpreter = interpreterOverride
	}

	// Check if the python interpreter exists in path
	_, err = exec.LookPath(pythonInterpreter)
	if err != nil && interpreterOverride != "" {
		return nil, fmt.Errorf("find python interpreter %s: %w", pythonInterpreter, err)
	} else if err != nil {
		slog.Debug("python interpreter not found, falling back", "interpreter", pythonInterpreter, "err", err)
		pythonInterpreter = "python"
		_, err = exec.LookPath(pythonInterpreter)
		if err != nil {
			return nil, fmt.Errorf("find python interpreter %s: %w", pythonInterpreter, err)
		}
	}

	pythonVersion, err := getPythonVersion(pythonInterpreter)
	if err != nil {
		return nil, fmt.Errorf("get python version: %w", err)
	}

	slog.Debug("using python interpreter", "version", pythonVersion)

	envID := generateEnvID(requirementsHash, pythonVersion)
	slog.Debug("generated environment id", "id", envID)

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
