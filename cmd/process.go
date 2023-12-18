package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

var ErrNoProcessFound = errors.New("no process found")

// findProcessWithPrefix finds a process with the given prefix in its command line
func findProcessWithPrefix(prefix string) (int, error) {
	d, err := os.Open("/proc")
	if err != nil {
		return 0, err
	}
	defer d.Close()

	for {
		names, err := d.Readdirnames(10)
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}

		for _, name := range names {
			// We only care if the name starts with a numeric
			if name[0] < '0' || name[0] > '9' {
				continue
			}

			// From this point forward, any errors we just ignore, because
			// it might simply be that the process doesn't exist anymore.
			pid, err := strconv.ParseInt(name, 10, 0)
			if err != nil {
				continue
			}

			cmdline, err := readCmdline(int(pid))
			if err != nil {
				continue
			}
			if strings.HasPrefix(cmdline, prefix) {
				return int(pid), nil
			}
		}
	}
	return 0, ErrNoProcessFound
}

// readCmdline reads the command line of a process
func readCmdline(pid int) (string, error) {
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	dataBytes, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return "", err
	}
	return string(dataBytes), nil
}
