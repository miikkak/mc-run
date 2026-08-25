package pluginupdate

import (
	"archive/zip"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// writeJar creates a jar-like zip file at path containing a
// velocity-plugin.json entry with the given id, plus a dummy class entry so
// it looks like a real plugin jar, not just a wrapped JSON file.
func writeJar(t *testing.T, path, id string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	w := zip.NewWriter(f)
	descriptor, err := w.Create(descriptorEntry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := descriptor.Write([]byte(`{"id":"` + id + `","version":"1.0"}`)); err != nil {
		t.Fatal(err)
	}
	class, err := w.Create("com/example/Plugin.class")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := class.Write([]byte("not real bytecode, just marker content: " + id)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeGarbage(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("not a zip file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func TestApply_NoUpdateDir(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, filepath.Join(dir, "foo-1.0.jar"), "foo")

	updates, err := Apply(dir, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("expected no updates, got %v", updates)
	}
}

func TestApply_UpdateDirIsAFile(t *testing.T) {
	dir := t.TempDir()
	// "update" exists but isn't a directory — Paper's own check
	// (updateDirectory.isDirectory()) silently ignores this too.
	if err := os.WriteFile(filepath.Join(dir, "update"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJar(t, filepath.Join(dir, "foo-1.0.jar"), "foo")

	updates, err := Apply(dir, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("expected no updates, got %v", updates)
	}
}

func TestApply_UpdateDirUnreadablePropagatesError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits don't block access")
	}

	dir := t.TempDir()
	updateDir := filepath.Join(dir, "update")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Deny execute on the parent so os.Stat(updateDir) itself fails with a
	// permission error rather than ErrNotExist — this must NOT be treated
	// as "no update folder."
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := Apply(dir, testLogger())
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if os.IsNotExist(err) {
		t.Fatalf("permission error should not be reported as ErrNotExist: %v", err)
	}
}

func TestApply_MatchesByDeclaredID(t *testing.T) {
	dir := t.TempDir()
	updateDir := filepath.Join(dir, "update")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldPath := filepath.Join(dir, "foo-1.0.jar")
	updatePath := filepath.Join(updateDir, "foo-2.0.jar")
	writeJar(t, oldPath, "foo")
	writeJar(t, updatePath, "foo")

	updates, err := Apply(dir, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d: %v", len(updates), updates)
	}

	newPath := filepath.Join(dir, "foo-2.0.jar")
	if updates[0] != (Update{ID: "foo", OldPath: oldPath, NewPath: newPath}) {
		t.Fatalf("unexpected update record: %+v", updates[0])
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old jar name should no longer exist, stat err = %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("renamed jar should exist: %v", err)
	}
	if _, err := os.Stat(updatePath); !os.IsNotExist(err) {
		t.Fatalf("update-folder source should have been removed, stat err = %v", err)
	}
}

func TestApply_PreservesOldFilePermissions(t *testing.T) {
	dir := t.TempDir()
	updateDir := filepath.Join(dir, "update")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldPath := filepath.Join(dir, "foo-1.0.jar")
	updatePath := filepath.Join(updateDir, "foo-2.0.jar")
	writeJar(t, oldPath, "foo")
	writeJar(t, updatePath, "foo")

	// Deliberately non-default so this can't pass by coincidentally
	// matching os.CreateTemp's own 0o600 default.
	if err := os.Chmod(oldPath, 0o640); err != nil {
		t.Fatal(err)
	}

	updates, err := Apply(dir, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d: %v", len(updates), updates)
	}

	info, err := os.Stat(updates[0].NewPath)
	if err != nil {
		t.Fatalf("stat updated jar: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o640 {
		t.Fatalf("expected updated jar to keep the old jar's 0640 permissions, got %#o", perm)
	}
}

func TestApply_SameFilenameOverwritesInPlace(t *testing.T) {
	dir := t.TempDir()
	updateDir := filepath.Join(dir, "update")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Old and update jar share the same filename, exercising the
	// newPath == pluginPath branch in replace().
	path := filepath.Join(dir, "foo.jar")
	updatePath := filepath.Join(updateDir, "foo.jar")
	writeJar(t, path, "foo")
	writeJar(t, updatePath, "foo")

	updates, err := Apply(dir, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d: %v", len(updates), updates)
	}
	if updates[0].NewPath != path {
		t.Fatalf("expected new path to equal old path, got %s", updates[0].NewPath)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("jar should still exist under its original name: %v", err)
	}
	if _, err := os.Stat(updatePath); !os.IsNotExist(err) {
		t.Fatalf("update-folder source should have been removed, stat err = %v", err)
	}
}

func TestApply_CleanupFailureStillCountsAsApplied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits don't block unlink")
	}

	dir := t.TempDir()
	updateDir := filepath.Join(dir, "update")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldPath := filepath.Join(dir, "foo-1.0.jar")
	updatePath := filepath.Join(updateDir, "foo-2.0.jar")
	writeJar(t, oldPath, "foo")
	writeJar(t, updatePath, "foo")

	// Deny write on updateDir so the post-rename os.Remove(updatePath)
	// fails, even though the rename that actually applies the update
	// (into dir, not updateDir) already succeeded.
	if err := os.Chmod(updateDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(updateDir, 0o755) })

	updates, err := Apply(dir, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected the update to still be recorded as applied despite the cleanup failure, got %d: %v", len(updates), updates)
	}

	newPath := filepath.Join(dir, "foo-2.0.jar")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new jar should exist with the update's content: %v", err)
	}
	// Confirm the leftover really is there (proving this test exercises
	// the cleanup-failure path, not a no-op).
	if _, err := os.Stat(updatePath); err != nil {
		t.Fatalf("expected the update-folder source to remain as an uncleaned leftover: %v", err)
	}
}

func TestApply_NoMatchingID(t *testing.T) {
	dir := t.TempDir()
	updateDir := filepath.Join(dir, "update")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldPath := filepath.Join(dir, "foo-1.0.jar")
	updatePath := filepath.Join(updateDir, "bar-2.0.jar")
	writeJar(t, oldPath, "foo")
	writeJar(t, updatePath, "bar")

	updates, err := Apply(dir, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("expected no updates, got %v", updates)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("unrelated plugin jar should be untouched: %v", err)
	}
	if _, err := os.Stat(updatePath); err != nil {
		t.Fatalf("unmatched update jar should be left in place: %v", err)
	}
}

func TestApply_SkipsUnreadableJarsAndAppliesRest(t *testing.T) {
	dir := t.TempDir()
	updateDir := filepath.Join(dir, "update")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeGarbage(t, filepath.Join(dir, "corrupt.jar"))
	writeGarbage(t, filepath.Join(updateDir, "corrupt-update.jar"))

	oldPath := filepath.Join(dir, "foo-1.0.jar")
	updatePath := filepath.Join(updateDir, "foo-2.0.jar")
	writeJar(t, oldPath, "foo")
	writeJar(t, updatePath, "foo")

	updates, err := Apply(dir, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(updates) != 1 || updates[0].ID != "foo" {
		t.Fatalf("expected the one valid update to apply despite garbage jars, got %v", updates)
	}
}

func TestApply_IgnoresNonJarFiles(t *testing.T) {
	dir := t.TempDir()
	updateDir := filepath.Join(dir, "update")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(updateDir, "README.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	updates, err := Apply(dir, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("expected no updates, got %v", updates)
	}
}

func TestApply_DuplicateIDsInUpdateFolderPicksFirstByName(t *testing.T) {
	dir := t.TempDir()
	updateDir := filepath.Join(dir, "update")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldPath := filepath.Join(dir, "foo-1.0.jar")
	writeJar(t, oldPath, "foo")
	// "a-foo.jar" sorts before "b-foo.jar"; the first one found should win.
	writeJar(t, filepath.Join(updateDir, "a-foo.jar"), "foo")
	writeJar(t, filepath.Join(updateDir, "b-foo.jar"), "foo")

	updates, err := Apply(dir, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d: %v", len(updates), updates)
	}
	if filepath.Base(updates[0].NewPath) != "a-foo.jar" {
		t.Fatalf("expected the alphabetically-first update jar to win, got %s", updates[0].NewPath)
	}
	if _, err := os.Stat(filepath.Join(updateDir, "b-foo.jar")); err != nil {
		t.Fatalf("unused duplicate should remain in the update folder: %v", err)
	}
}
