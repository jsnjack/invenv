package cmd

import (
	"fmt"
	"os"
	"path"
	"strings"
)

// Script represents a Python script
type Script struct {
	AbsolutePath string
	EnvDir       string
}

// CreateEnv creates a virtual environment for the script
func (s *Script) CreateEnv() error {
	var err error
	if flagDebug {
		err = execCmd("python", "-m", "venv", s.EnvDir)
	} else {
		err = execCmdSilent("python", "-m", "venv", s.EnvDir)
	}
	if err != nil {
		return fmt.Errorf("failed to create virtual environment: %s", err)
	}
	return nil
}

// InstallRequirements installs the requirements for the script
func (s *Script) InstallRequirements() error {
	// Try guess the unique requirements file name
	scriptFile := path.Base(s.AbsolutePath)
	scriptDir := path.Dir(s.AbsolutePath)
	scriptFile = strings.TrimSuffix(scriptFile, ".py")
	requirementsFile := path.Join(scriptDir, "requirements_"+scriptFile+".txt")
	if flagDebug {
		fmt.Printf("Assuming requirements file %s...\n", requirementsFile)
	}
	_, err := os.Stat(requirementsFile)
	if err == nil {
		err := s.installRequirementsInEnv(requirementsFile)
		if err == nil {
			if flagDebug {
				fmt.Printf("Installed requirements from %s file\n", requirementsFile)
			}
			return nil
		} else {
			if flagDebug {
				fmt.Printf("Failed to install requirements from %s file: %s\n", requirementsFile, err)
			}
		}
	}

	// Try to use requirements.txt
	requirementsFile = path.Join(scriptDir, "requirements.txt")
	if flagDebug {
		fmt.Printf("Assuming requirements file %s...\n", requirementsFile)
	}
	_, err = os.Stat(requirementsFile)
	if err == nil {
		err := s.installRequirementsInEnv(requirementsFile)
		if err == nil {
			if flagDebug {
				fmt.Printf("Installed requirements from %s file\n", requirementsFile)
			}
			return nil
		} else {
			if flagDebug {
				fmt.Printf("Failed to install requirements from %s file: %s\n", requirementsFile, err)
			}
		}
	}
	if flagDebug {
		fmt.Println("No requirements file found")
	}
	return nil
}

func (s *Script) installRequirementsInEnv(filename string) error {
	var err error
	if flagDebug {
		err = execCmd(path.Join(s.EnvDir, "bin/pip"), "install", "--no-input", "-r", filename)
	} else {
		err = execCmdSilent(path.Join(s.EnvDir, "bin/pip"), "install", "--no-input", "-r", filename)
	}
	if err != nil {
		return fmt.Errorf("failed to install requirements: %s", err)
	}
	return nil
}

// NewScript creates a new Script instance
func NewScript(scriptName string) (*Script, error) {
	absPath := scriptName
	if !path.IsAbs(scriptName) {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		absPath = path.Join(cwd, scriptName)
	}

	// Check if the script exists
	_, err := os.Stat(absPath)
	if err != nil {
		return nil, err
	}

	envDir, err := generateEnvDirName(absPath)
	if err != nil {
		return nil, err
	}

	if flagDebug {
		fmt.Printf("Env dir: %s\n", envDir)
	}

	script := &Script{
		AbsolutePath: absPath,
		EnvDir:       envDir,
	}
	return script, nil
}
