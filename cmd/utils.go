package cmd

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
)

// generateEnvDirName generates a name for the directory that will contain the virtual environment
func generateEnvDirName(filename string) (string, error) {
	// Calculate hash of the script name
	hasher := sha256.New()
	hasher.Write([]byte(filename))
	hashBS := hasher.Sum(nil)
	hashStr := fmt.Sprintf("%x", hashBS)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// Env dir name contains hash of the script name (to support different locations),
	// and the script name itself (with dots replaced with dashes). Finally, it includes
	// the ".env" postfix.
	finalComp := hashStr + "_" + strings.ReplaceAll(path.Base(filename), ".", "-") + ".env"
	envDir := path.Join(homeDir, ".local", "ave", finalComp)
	return envDir, nil
}

// ensureDependency ensures that the dependency is installed
func ensureDependency(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("%s is not installed: %s", name, err)
	}
	return nil
}

func ensureAllDependencies() error {
	err := ensureDependency("python3", "--version")
	if err != nil {
		return err
	}

	err = ensureDependency("virtualenv", "--version")
	if err != nil {
		return err
	}

	err = ensureDependency("pip3", "--version")
	if err != nil {
		return err
	}
	return nil
}
