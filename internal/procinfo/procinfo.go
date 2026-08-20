// Package procinfo answers process-introspection questions by reading /proc
// directly, for container images that don't ship procps (no pgrep/ps).
package procinfo

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ErrNotFound is returned when no process or status field matches the query.
var ErrNotFound = errors.New("procinfo: not found")

// FindPIDByComm scans procRoot (typically "/proc") for a process whose comm
// (the kernel-truncated command name in /proc/<pid>/comm) equals name
// exactly, returning the lowest matching PID. It returns ErrNotFound if no
// process matches.
func FindPIDByComm(procRoot, name string) (int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, fmt.Errorf("procinfo: read %s: %w", procRoot, err)
	}

	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // not a PID directory
		}
		pids = append(pids, pid)
	}
	sort.Ints(pids)

	for _, pid := range pids {
		comm, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "comm"))
		if err != nil {
			continue // process exited between readdir and read, or unreadable
		}
		if strings.TrimSpace(string(comm)) == name {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("procinfo: no process named %q: %w", name, ErrNotFound)
}

// StatusField reads /proc/<pid>/status under procRoot and returns the first
// value on the line for field (e.g. "Uid" or "Gid" returns the real ID,
// the first of the four space-separated values on that line).
func StatusField(procRoot string, pid int, field string) (string, error) {
	path := filepath.Join(procRoot, strconv.Itoa(pid), "status")
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("procinfo: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	prefix := field + ":"
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, prefix))
		if len(fields) == 0 {
			return "", fmt.Errorf("procinfo: %s line has no value in %s: %w", field, path, ErrNotFound)
		}
		return fields[0], nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("procinfo: read %s: %w", path, err)
	}
	return "", fmt.Errorf("procinfo: no %s field in %s: %w", field, path, ErrNotFound)
}
