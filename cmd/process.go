package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ErrNoProcessFound is returned when no running process uses the environment.
var ErrNoProcessFound = errors.New("no process uses the environment")

// findProcessWithPrefix finds a process whose argv[0] starts with prefix.
// invenv always execs the interpreter with argv[0] set to the full path
// inside the env dir (see cmd_root.go), so this identifies processes
// actually running as that environment's python — not merely processes
// that happen to mention the path in a later argument (e.g. a "cat" or
// editor opened on a file inside the env).
func findProcessWithPrefix(prefix string) (int, error) {
	d, err := os.Open("/proc")
	if err != nil {
		return 0, fmt.Errorf("open /proc: %w", err)
	}
	defer func() {
		if err := d.Close(); err != nil {
			trace("close /proc", "err", err)
		}
	}()

	for {
		names, err := d.Readdirnames(10)
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("read /proc entries: %w", err)
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

			argv0, err := readCmdlineArgv0(int(pid))
			if err != nil {
				continue
			}
			if strings.HasPrefix(argv0, prefix) {
				return int(pid), nil
			}
		}
	}
	return 0, ErrNoProcessFound
}

// readCmdlineArgv0 returns argv[0] of a process's command line.
// /proc/[pid]/cmdline separates arguments with null bytes, not spaces, so
// splitting (or replacing them) on whitespace would misparse an argument
// that itself contains a space — env dir paths are not guaranteed to be
// space-free.
func readCmdlineArgv0(pid int) (string, error) {
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	dataBytes, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return "", fmt.Errorf("read cmdline: %w", err)
	}
	argv0, _, _ := bytes.Cut(dataBytes, []byte{0})
	return string(argv0), nil
}
