package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// LockAcquireAttempts is the number of attempts to acquire the lock. Also
// correlates with the number of seconds to wait for the lock.
const LockAcquireAttempts = 180

// Script represents a Python script
type Script struct {
	AbsolutePath      string    // Full path to the script
	EnvDir            string    // Full path to the virtual environment
	PythonInterpreter string    // Full path to the Python interpreter
	VEnv              *VEnvInfo // Virtual environment information
}

// CreateEnv creates a virtual environment for the script
func (s *Script) CreateEnv(forceNewEnv bool) error {
	var err error
	var output []string

	// Delete the old virtual environment if requested
	if forceNewEnv {
		err = s.RemoveVEnv()
		if err != nil {
			return err
		}
	}

	err = s.VerifyExistingEnv()
	if err == nil {
		if flagDebug {
			loggerErr.Println("Using existing virtual environment")
		}
		return nil
	} else {
		if flagDebug {
			loggerErr.Printf("Failed to verify existing virtual environment: %s\n", err)
		}
		err = s.RemoveVEnv()
		if err != nil {
			return err
		}
	}

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
	pythonVersion, err := getPythonVersion(s.PythonInterpreter)
	if err != nil {
		return err
	}
	newVenv := &VEnvInfo{
		PythonInterpreter: s.PythonInterpreter,
		PythonVersion:     pythonVersion,
	}
	s.VEnv = newVenv
	return nil
}

// VerifyExistingEnv verifies that the existing virtual environment is valid
func (s *Script) VerifyExistingEnv() error {
	// Check if the virtual environment already exists
	_, err := os.Stat(s.EnvDir)
	if err != nil {
		return err
	}

	if s.VEnv == nil {
		return fmt.Errorf("virtual environment info is not available")
	}

	// Verify that the the existing virtual environment has the same Python interpreter
	// as the script
	if s.VEnv.PythonInterpreter != s.PythonInterpreter {
		return fmt.Errorf("existing virtual environment has %s, want %s", s.VEnv.PythonInterpreter, s.PythonInterpreter)
	}

	// Verify that the Python version used to create the virtual environment is the same
	// as the current Python version
	currentPythonVersion, err := getPythonVersion(s.VEnv.PythonInterpreter)
	if err != nil {
		return err
	}

	if currentPythonVersion != s.VEnv.PythonVersion {
		return fmt.Errorf("existing virtual environment has python %s, want %s", currentPythonVersion, s.VEnv.PythonVersion)
	}
	return nil
}

// GuessAndInstallRequirements installs the requirements for the script by guessing
// the requirements file name
func (s *Script) GuessAndInstallRequirements() error {
	// Try guess the unique requirements file name
	scriptFile := path.Base(s.AbsolutePath)
	scriptDir := path.Dir(s.AbsolutePath)
	scriptFile = strings.TrimSuffix(scriptFile, ".py")

	guesses := []string{
		"requirements_" + scriptFile + ".txt",
		scriptFile + "_requirements.txt",
		"requirements.txt",
	}

	for _, guess := range guesses {
		requirementsFile := path.Join(scriptDir, guess)
		if flagDebug {
			loggerErr.Printf("Assuming requirements file %s...\n", requirementsFile)
		}
		_, err := os.Stat(requirementsFile)
		if err == nil {
			return s.InstallRequirementsInEnv(requirementsFile)
		} else {
			if flagDebug {
				loggerErr.Println(err)
			}
		}
	}

	if flagDebug {
		loggerErr.Println("No requirements file found")
	}
	// Save the hash of the requirements file
	s.VEnv.RequirementsHash = ""
	err := s.VEnv.Save(s.EnvDir)
	return err
}

func (s *Script) InstallRequirementsInEnv(filename string) error {
	var err error
	var output []string

	// Check if the requirements file has changed
	newReqFileHash, err := getFileHash(filename)
	if err != nil {
		return err
	}
	if newReqFileHash == s.VEnv.RequirementsHash {
		if flagDebug {
			loggerErr.Println("Requirements file has not changed")
		}
		return nil
	}

	if flagDebug {
		err = execCmd(path.Join(s.EnvDir, "bin/pip"), "install", "--no-input", "-r", filename)
	} else {
		output, err = execCmdSilent(path.Join(s.EnvDir, "bin/pip"), "install", "--no-input", "-r", filename)
	}
	if err != nil {
		// Print buffered combined output if the command failed
		if !flagDebug {
			loggerErr.Println("\n", strings.Join(output, "\n"))
		}
		return fmt.Errorf("failed to install requirements: %s", err)
	}

	// Save the hash of the requirements file
	s.VEnv.RequirementsHash = newReqFileHash
	err = s.VEnv.Save(s.EnvDir)
	return err
}

func (s *Script) RemoveVEnv() error {
	if flagDebug {
		loggerErr.Println("Deleting old virtual environment...")
	}
	err := removeDir(s.EnvDir)
	s.VEnv = nil
	return err
}

// NewScript creates a new Script instance
func NewScript(scriptName string, interpreterOverride string) (*Script, error) {
	scriptPath, err := filepath.Abs(scriptName)
	if err != nil {
		return nil, err
	}

	// Check if the script exists
	_, err = os.Stat(scriptPath)
	if err != nil {
		return nil, err
	}

	envDir, err := generateEnvDirName(scriptPath)
	if err != nil {
		return nil, err
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
	pythonAbsPath, err := exec.LookPath(pythonInterpreter)
	if err != nil && interpreterOverride != "" {
		return nil, fmt.Errorf("failed to find python interpreter %s: %s", pythonInterpreter, err)
	} else if err != nil {
		if flagDebug {
			loggerErr.Printf("Failed to find python interpreter %s: %s, assuming `python`...\n", pythonInterpreter, err)
		}
		pythonInterpreter = "python"
		pythonAbsPath, err = exec.LookPath(pythonInterpreter)
		if err != nil {
			return nil, fmt.Errorf("failed to find python interpreter %s: %s", pythonInterpreter, err)
		}
	}

	script := &Script{
		AbsolutePath:      scriptPath,
		EnvDir:            envDir,
		PythonInterpreter: pythonAbsPath,
	}
	venv, err := NewVenvInfo(script.EnvDir)
	if err != nil && flagDebug {
		loggerErr.Printf("Failed to read virtual environment info: %s\n", err)
	}
	script.VEnv = venv
	if flagDebug {
		loggerErr.Println("Parsing completed.")
		loggerErr.Printf("Script: %s\n", script.AbsolutePath)
		loggerErr.Printf("Directory with environment: %s\n", script.EnvDir)
		loggerErr.Printf("Python interpreter: %s\n", script.PythonInterpreter)
	}
	return script, nil
}
