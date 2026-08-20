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

func TestFindPIDByCommLowestMatch(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 100, "java", "")
	writeProc(t, root, 8, "java", "")

	pid, err := FindPIDByComm(root, "java")
	if err != nil {
		t.Fatalf("FindPIDByComm: %v", err)
	}
	if pid != 8 {
		t.Errorf("got pid %d, want 8 (lowest)", pid)
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
