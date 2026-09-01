package procinfo

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeProc(t *testing.T, root string, pid int, comm, status string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if comm != "" {
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if status != "" {
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindPIDByComm(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 1, "mc-run", "")
	writeProc(t, root, 42, "java", "")
	writeProc(t, root, 7, "bash", "")
	// non-PID entry should be ignored
	if err := os.MkdirAll(filepath.Join(root, "self"), 0o755); err != nil {
		t.Fatal(err)
	}

	pid, err := FindPIDByComm(root, "java")
	if err != nil {
		t.Fatalf("FindPIDByComm: %v", err)
	}
	if pid != 42 {
		t.Errorf("got pid %d, want 42", pid)
	}
}

// TestFindPIDByCommExcludesSelf guards against a lookup that queries its
// own comm always matching (or being reported ambiguous alongside a real
// target) just because the querying process itself shows up in the /proc
// scan — e.g. `mc-run pid --name mc-run` run from inside the same PID
// namespace as the container's PID 1.
func TestFindPIDByCommExcludesSelf(t *testing.T) {
	root := t.TempDir()
	self := os.Getpid()
	// Derived from self (not hard-coded) so it can never collide with the
	// running test process's own PID, however small that's possible.
	target := self + 1
	writeProc(t, root, self, "mc-run", "")
	writeProc(t, root, target, "mc-run", "")

	pid, err := FindPIDByComm(root, "mc-run")
	if err != nil {
		t.Fatalf("FindPIDByComm: %v", err)
	}
	if pid != target {
		t.Errorf("got pid %d, want %d (self pid %d should have been excluded)", pid, target, self)
	}
}

// TestFindPIDByCommTruncatesLongNames guards against a query name longer
// than TASK_COMM_LEN-1 (15 bytes) never matching: the kernel truncates
// /proc/<pid>/comm to that length, so FindPIDByComm must compare against
// the same truncated form to find e.g. a JVM launched under a long name.
func TestFindPIDByCommTruncatesLongNames(t *testing.T) {
	root := t.TempDir()
	longName := "a-very-long-process-name-that-exceeds-the-kernel-limit"
	truncated := longName[:15]
	// Derived from self (not hard-coded) so it can never collide with the
	// running test process's own PID, which FindPIDByComm now excludes.
	target := os.Getpid() + 1
	writeProc(t, root, target, truncated, "")

	pid, err := FindPIDByComm(root, longName)
	if err != nil {
		t.Fatalf("FindPIDByComm: %v", err)
	}
	if pid != target {
		t.Errorf("got pid %d, want %d", pid, target)
	}
}

func TestFindPIDByCommAmbiguous(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 100, "java", "")
	writeProc(t, root, 8, "java", "")

	_, err := FindPIDByComm(root, "java")
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("got err %v, want ErrAmbiguous", err)
	}
}

func TestFindPIDByCommNotFound(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 1, "mc-run", "")

	_, err := FindPIDByComm(root, "java")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got err %v, want ErrNotFound", err)
	}
}

func TestStatusField(t *testing.T) {
	root := t.TempDir()
	status := "Name:\tjava\nUid:\t1000\t1000\t1000\t1000\nGid:\t1000\t1000\t1000\t1000\n"
	writeProc(t, root, 1, "", status)

	uid, err := StatusField(root, 1, "Uid")
	if err != nil {
		t.Fatalf("StatusField(Uid): %v", err)
	}
	if uid != "1000" {
		t.Errorf("got uid %q, want %q", uid, "1000")
	}

	gid, err := StatusField(root, 1, "Gid")
	if err != nil {
		t.Fatalf("StatusField(Gid): %v", err)
	}
	if gid != "1000" {
		t.Errorf("got gid %q, want %q", gid, "1000")
	}
}

// TestStatusFieldCaseInsensitive guards against StatusField failing to find
// a field due to case differences between the requested field name and
// /proc/<pid>/status's own capitalization (e.g. "uid" vs "Uid").
func TestStatusFieldCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	status := "Name:\tjava\nUid:\t1000\t1000\t1000\t1000\n"
	writeProc(t, root, 1, "", status)

	uid, err := StatusField(root, 1, "uid")
	if err != nil {
		t.Fatalf("StatusField(uid): %v", err)
	}
	if uid != "1000" {
		t.Errorf("got uid %q, want %q", uid, "1000")
	}
}

func TestStatusFieldMissing(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 1, "", "Name:\tjava\n")

	_, err := StatusField(root, 1, "Uid")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got err %v, want ErrNotFound", err)
	}
}

func TestStatusFieldNoSuchPID(t *testing.T) {
	root := t.TempDir()

	_, err := StatusField(root, 999, "Uid")
	if err == nil {
		t.Error("expected error for missing pid, got nil")
	}
}
