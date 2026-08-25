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
