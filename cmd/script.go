package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const RequirementsHashFilename = ".previous_requirements_hash"

// Script represents a Python script
type Script struct {
	AbsolutePath string // Full path to the script
	EnvDir       string // Full path to the virtual environment
	Python       string // Full path to the Python interpreter
}

// CreateEnv creates a virtual environment for the script
func (s *Script) CreateEnv(forceNewEnv bool) error {
	var err error

	// Delete the old virtual environment if requested
	if forceNewEnv {
		if flagDebug {
			loggerErr.Println("Deleting old virtual environment")
		}
		err = os.RemoveAll(s.EnvDir)
		if err != nil {
			return fmt.Errorf("failed to delete old virtual environment: %s", err)
		}
	}

	// Check if the virtual environment already exists
	_, err = os.Stat(s.EnvDir)
	if err == nil {
		if flagDebug {
			loggerErr.Println("Virtual environment already exists")
		}
		return nil
	}

	err = ensureAllSystemDependencies()
	if err != nil {
		return err
	}

	// Ensure python interpreter
	err = ensureDependency(s.Python, "--version")
	if err != nil {
		return err
	}

	if flagDebug {
		err = execCmd("virtualenv", "--python", s.Python, s.EnvDir)
	} else {
		err = execCmdSilent("virtualenv", "--python", s.Python, s.EnvDir)
	}
	if err != nil {
		return fmt.Errorf("failed to create virtual environment: %s", err)
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
	return nil
}

func (s *Script) InstallRequirementsInEnv(filename string) error {
	var err error

	// Check if the requirements file has changed
	newReqFileHash, err := getFileHash(filename)
	if err != nil {
		return err
	}
	oldReqFileHash, err := os.ReadFile(path.Join(s.EnvDir, RequirementsHashFilename))
	if err == nil && newReqFileHash == string(oldReqFileHash) {
		if flagDebug {
			loggerErr.Println("Requirements file has not changed")
		}
		return nil
	}

	if flagDebug {
		err = execCmd(path.Join(s.EnvDir, "bin/pip"), "install", "--no-input", "-r", filename)
	} else {
		err = execCmdSilent(path.Join(s.EnvDir, "bin/pip"), "install", "--no-input", "-r", filename)
	}
	if err != nil {
		return fmt.Errorf("failed to install requirements: %s", err)
	}

	// Save the hash of the requirements file
	errHashFileWrite := os.WriteFile(path.Join(s.EnvDir, RequirementsHashFilename), []byte(newReqFileHash), 0644)
	if errHashFileWrite != nil && flagDebug {
		loggerErr.Printf("Failed to save hash of the requirements file: %s\n", errHashFileWrite)
	}
	return nil
}

// NewScript creates a new Script instance
func NewScript(scriptName string) (*Script, error) {
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

	extractedPython, err := extractPythonFromShebang(scriptPath)
	if err != nil {
		if flagDebug {
			loggerErr.Printf("Failed to extract python from shebang: %s\n", err)
		}
	}

	if extractedPython == "" {
		extractedPython = "python"
	}

	// Check if the python interpreter exists in path
	pythonAbsPath, err := exec.LookPath(extractedPython)
	if err != nil {
		return nil, fmt.Errorf("failed to find python interpreter %s: %s", extractedPython, err)
	}

	script := &Script{
		AbsolutePath: scriptPath,
		EnvDir:       envDir,
		Python:       pythonAbsPath,
	}
	if flagDebug {
		loggerErr.Println("Parsing completed.")
		loggerErr.Printf("Script: %s\n", script.AbsolutePath)
		loggerErr.Printf("Directory with environment: %s\n", script.EnvDir)
		loggerErr.Printf("Python interpreter: %s\n", script.Python)
	}
	return script, nil
}
