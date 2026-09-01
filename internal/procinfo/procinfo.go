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

// ErrAmbiguous is returned when a comm-name query matches more than one
// process, so the caller can't safely act on a single PID (e.g. jcmd's
// attach protocol targets exactly one process).
var ErrAmbiguous = errors.New("procinfo: ambiguous")

// FindPIDByComm scans procRoot (typically "/proc") for processes whose comm
// (the kernel-truncated command name in /proc/<pid>/comm) equals name
// exactly. It returns ErrNotFound if none match, and ErrAmbiguous if more
// than one does — callers that need a single, unambiguous target (like
// jcmd) should not fall back to picking the lowest PID silently.
func FindPIDByComm(procRoot, name string) (int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, fmt.Errorf("procinfo: read %s: %w", procRoot, err)
	}

	// Linux truncates /proc/<pid>/comm to TASK_COMM_LEN-1 (15) bytes, so a
	// longer target name can never match verbatim — compare against the
	// same truncated form the kernel would report, mirroring its behavior.
	const commLen = 15
	if len(name) > commLen {
		name = name[:commLen]
	}

	self := os.Getpid()
	var candidates []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // not a PID directory
		}
		if pid == self {
			// Never match the process running this very lookup: e.g. `mc-run
			// pid --name mc-run` querying its own comm would otherwise
			// always report itself as an ambiguous (or the only) match.
			continue
		}
		candidates = append(candidates, pid)
	}
	sort.Ints(candidates)

	var matches []int
	for _, pid := range candidates {
		comm, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "comm"))
		if err != nil {
			continue // process exited between readdir and read, or unreadable
		}
		if strings.TrimSpace(string(comm)) == name {
			matches = append(matches, pid)
		}
	}

	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("procinfo: no process named %q: %w", name, ErrNotFound)
	case 1:
		return matches[0], nil
	default:
		return 0, fmt.Errorf("procinfo: multiple processes named %q (pids %v): %w", name, matches, ErrAmbiguous)
	}
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

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		lineField, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(lineField, field) {
			continue
		}
		fields := strings.Fields(value)
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
