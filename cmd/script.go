package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const VEnvInfoFilename = ".venv.version"
const VEnvDirDefaultName = ".venv"

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

	_, err := os.Stat(s.EnvDir)
	if err != nil {
		if os.IsNotExist(err) {
			readOperationOnly = false
		}
	}

	if s.fromInitCommand && readOperationOnly {
		// If the script was created with init command, it doesn't have a unique
		// environment ID as part of its path, so we can't rely on the presence of
		// the environment directory to determine if it exists.
		infoFilename := path.Join(s.EnvDir, VEnvInfoFilename)
		data, err := os.ReadFile(infoFilename)
		if err != nil {
			readOperationOnly = false
			if flagDebug {
				loggerErr.Printf("Failed to read environment info file: %s\n", err)
			}
		} else {
			if strings.TrimSpace(string(data)) != s.venvID {
				// Environment ID mismatch, recreate the environment
				readOperationOnly = false
				deleteOldEnv = true
				if flagDebug {
					loggerErr.Printf("Environment ID mismatch: got %s, want %s\n", string(data), s.venvID)
				}
			}
		}
	}

	err = waitUntilEnvIsUnlocked(s.EnvDir)
	switch {
	case err == nil:
		break
	case errors.Is(err, ErrNoProcessFound), errors.Is(err, errStaleLockfile):
		if flagDebug {
			loggerErr.Printf("recreating environment: %s\n", err)
		}
		// Environment is locked at the moment, but most likely incorrectly.
		// Unlock it and recreate the environment
		readOperationOnly = false
		deleteOldEnv = true
	default:
		// Unhandled error occured
		return err
	}

	if !readOperationOnly {
		lockEnv(s.EnvDir)
		defer unlockEnv(s.EnvDir)
		if deleteOldEnv {
			err = s.RemoveEnv()
			if err != nil {
				return err
			}
		}
		err = s.CreateEnv()
		if err != nil {
			return err
		}
		err = s.InstallRequirementsInEnv()
		if err != nil {
			// If the installation failed, remove the environment so we don't
			// leave a broken environment behind and other scripts won't use it
			s.RemoveEnv()
			return err
		}
		if s.fromInitCommand {
			// Write the environment ID to the info file
			infoFilename := path.Join(s.EnvDir, VEnvInfoFilename)
			err = os.WriteFile(infoFilename, []byte(s.venvID), 0644)
			if err != nil {
				return err
			}
			if flagDebug {
				loggerErr.Printf("Wrote environment ID to %s\n", infoFilename)
			}
		}
		return nil
	}
	return nil
}

// CreateEnv creates a virtual environment for the script
func (s *Script) CreateEnv() error {
	var err error
	var output []string

	if flagDebug {
		loggerErr.Println("Creating new virtual environment...")
	}

	// First, try to use venv module
	err = exec.Command(s.PythonInterpreter, "-m", "venv", "--help").Run()
	if err == nil {
		if flagDebug {
			loggerErr.Println("Using venv module...")
			err = execCmd(s.PythonInterpreter, "-m", "venv", s.EnvDir)
		} else {
			output, err = execCmdSilent(s.PythonInterpreter, "-m", "venv", s.EnvDir)
		}
	} else {
		// Ensure virtualenv is installed
		var virtualenvPath string
		virtualenvPath, err = exec.LookPath("virtualenv")
		if err != nil {
			return fmt.Errorf("failed to find virtualenv: %s", err)
		}
		if flagDebug {
			loggerErr.Println("Using virtualenv...")
			err = execCmd(virtualenvPath, "--python", s.PythonInterpreter, s.EnvDir)
		} else {
			output, err = execCmdSilent(virtualenvPath, "--python", s.PythonInterpreter, s.EnvDir)
		}
	}
	if err != nil {
		// Print buffered combined output if the command failed
		if !flagDebug {
			loggerErr.Println("\n", strings.Join(output, "\n"))
		}
		return fmt.Errorf("failed to create virtual environment: %s", err)
	}
	return nil
}

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
			loggerErr.Println("\n", strings.Join(output, "\n"))
		}
		return fmt.Errorf("failed to install requirements: %s", err)
	}
	return err
}

func (s *Script) RemoveEnv() error {
	if flagDebug {
		loggerErr.Println("Deleting virtual environment...")
	}
	err := removeDir(s.EnvDir)
	return err
}

// NewScript creates a new Script instance
func NewScript(scriptName string, interpreterOverride string, requirementsOverride string) (*Script, error) {
	scriptPath, err := filepath.Abs(scriptName)
	if err != nil {
		return nil, err
	}

	// Check if the script exists
	_, err = os.Stat(scriptPath)
	if err != nil {
		return nil, err
	}

	// Try to find requirements.txt file for the script
	requirementsFile, err := getRequirementsFileForScript(scriptPath, requirementsOverride)
	if err != nil {
		return nil, err
	}

	if flagDebug {
		if requirementsFile == "" {
			loggerErr.Println("No requirements file found")
		} else {
			loggerErr.Println("Found requirements file: ", requirementsFile)
		}
	}

	requirementsHash := ""
	if requirementsFile != "" {
		requirementsHash, err = getFileHash(requirementsFile)
		if err != nil {
			return nil, err
		}
	}

	if flagDebug {
		loggerErr.Printf("Requirements file hash: %s\n", requirementsHash)
	}

	var pythonInterpreter string
	if interpreterOverride == "" {
		pythonInterpreter, err = extractPythonFromShebang(scriptPath)
		if err != nil {
			if flagDebug {
				loggerErr.Printf("Failed to extract python from shebang: %s\n", err)
			}
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
		return nil, fmt.Errorf("failed to find python interpreter %s: %s", pythonInterpreter, err)
	} else if err != nil {
		if flagDebug {
			loggerErr.Printf("Failed to find python interpreter %s: %s, assuming `python`...\n", pythonInterpreter, err)
		}
		pythonInterpreter = "python"
		_, err = exec.LookPath(pythonInterpreter)
		if err != nil {
			return nil, fmt.Errorf("failed to find python interpreter %s: %s", pythonInterpreter, err)
		}
	}

	pythonVersion, err := getPythonVersion(pythonInterpreter)
	if err != nil {
		return nil, err
	}

	if flagDebug {
		loggerErr.Printf("Using python interpreter: %s\n", pythonVersion)
	}

	envID := generateEnvID(requirementsHash, pythonVersion)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	envDir := path.Join(homeDir, EnvironmentsDir, envID+".env")

	if flagDebug {
		loggerErr.Println("Using virtual environment: ", envDir)
	}

	script := &Script{
		AbsolutePath:      scriptPath,
		EnvDir:            envDir,
		PythonInterpreter: pythonInterpreter,
		RequirementsPath:  requirementsFile,
		venvID:            envID,
	}
	return script, nil
}

// NewInitCmd creates a new Script instance
func NewInitCmd(interpreterOverride string, requirementsOverride string) (*Script, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// Try to find requirements.txt file for the script
	requirementsFile, err := getRequirementsFileForScript(path.Join(cwd, ".placeholder"), requirementsOverride)
	if err != nil {
		return nil, err
	}

	if flagDebug {
		if requirementsFile == "" {
			loggerErr.Println("No requirements file found")
		} else {
			loggerErr.Println("Found requirements file: ", requirementsFile)
		}
	}

	requirementsHash := ""
	if requirementsFile != "" {
		requirementsHash, err = getFileHash(requirementsFile)
		if err != nil {
			return nil, err
		}
	}

	if flagDebug {
		loggerErr.Printf("Requirements file hash: %s\n", requirementsHash)
	}

	var pythonInterpreter string
	if interpreterOverride == "" {
		pythonInterpreter = "python"
	} else {
		pythonInterpreter = interpreterOverride
	}

	// Check if the python interpreter exists in path
	_, err = exec.LookPath(pythonInterpreter)
	if err != nil && interpreterOverride != "" {
		return nil, fmt.Errorf("failed to find python interpreter %s: %s", pythonInterpreter, err)
	} else if err != nil {
		if flagDebug {
			loggerErr.Printf("Failed to find python interpreter %s: %s, assuming `python`...\n", pythonInterpreter, err)
		}
		pythonInterpreter = "python"
		_, err = exec.LookPath(pythonInterpreter)
		if err != nil {
			return nil, fmt.Errorf("failed to find python interpreter %s: %s", pythonInterpreter, err)
		}
	}

	pythonVersion, err := getPythonVersion(pythonInterpreter)
	if err != nil {
		return nil, err
	}

	if flagDebug {
		loggerErr.Printf("Using python interpreter: %s\n", pythonVersion)
	}

	envID := generateEnvID(requirementsHash, pythonVersion)
	if flagDebug {
		loggerErr.Printf("Generated environment ID: %s\n", envID)
	}

	envDir := path.Join(cwd, VEnvDirDefaultName)

	if flagDebug {
		loggerErr.Println("Using virtual environment: ", envDir)
	}

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
