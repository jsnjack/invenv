package cmd

import (
	"os"
	"path"

	"gopkg.in/yaml.v3"
)

// VEnvInfoFilename is the name of the file that contains information about the
// virtual environment.
const VEnvInfoFilename = "invenv.yaml"

// VEnvInfo represents information about a virtual environment. It is used to verify
// that the virtual environment is still valid and up-to-date.
type VEnvInfo struct {
	RequirementsHash  string `yaml:"requirements_hash"`  // Hash of the requirements file
	PythonInterpreter string `yaml:"python_interpreter"` // Full path to the Python interpreter, as requested by the script
	PythonVersion     string `yaml:"python_version"`     // Python version
}

func (v *VEnvInfo) Save(dir string) error {
	filename := path.Join(dir, VEnvInfoFilename)
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	err = os.WriteFile(filename, data, 0644)
	if err != nil {
		return err
	}
	return nil
}

func NewVenvInfo(dir string) (*VEnvInfo, error) {
	filename := path.Join(dir, VEnvInfoFilename)
	// Check if the file exists
	_, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}

	// Read the file
	venvInfo := &VEnvInfo{}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(data, &venvInfo)
	if err != nil {
		return nil, err
	}
	return venvInfo, nil
}
