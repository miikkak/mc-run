package plugininstall

import (
	"archive/zip"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// writeJarWithEntries creates a jar-like zip file at path containing the
// given zero-byte-content descriptor entries plus a dummy class entry, so
// it looks like a real plugin jar rather than a wrapped descriptor file.
func writeJarWithEntries(t *testing.T, path string, entries ...string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	w := zip.NewWriter(f)
	for _, name := range entries {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("marker content for " + name)); err != nil {
			t.Fatal(err)
		}
	}
	class, err := w.Create("com/example/Plugin.class")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := class.Write([]byte("not real bytecode")); err != nil {
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

// TestWriteTempFile_CleansUpOnCopyFailure guards against leaking abandoned
// .plugininstall-*.jar.tmp files in the plugins directory when staging
// fails partway through (e.g. a read error copying from src).
func TestWriteTempFile_CleansUpOnCopyFailure(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "not-a-file")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := writeTempFile(dir, srcDir, 0o644); err == nil {
		t.Fatal("expected writeTempFile to fail copying from a directory")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "not-a-file" {
			t.Fatalf("expected no leftover temp file in %s, found %s", dir, e.Name())
		}
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func TestApply_NoInstallDir(t *testing.T) {
	dir := t.TempDir()

	installs, err := Apply(dir, Velocity, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 0 {
		t.Fatalf("expected no installs, got %v", installs)
	}
}

func TestApply_InstallDirIsAFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "install"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	installs, err := Apply(dir, Velocity, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 0 {
		t.Fatalf("expected no installs, got %v", installs)
	}
}

func TestApply_InstallDirUnreadablePropagatesError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits don't block access")
	}

	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := Apply(dir, Velocity, testLogger())
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if os.IsNotExist(err) {
		t.Fatalf("permission error should not be reported as ErrNotExist: %v", err)
	}
}

func TestApply_UnknownServerTypeErrors(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJarWithEntries(t, filepath.Join(installDir, "foo.jar"), "velocity-plugin.json")

	_, err := Apply(dir, ServerType(99), testLogger())
	if err == nil {
		t.Fatal("expected an error for an unknown ServerType, got nil")
	}
}

func TestApply_VelocityJarInstallsUnderVelocityOnly(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jarPath := filepath.Join(installDir, "foo.jar")
	writeJarWithEntries(t, jarPath, "velocity-plugin.json")

	installs, err := Apply(dir, Paper, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 0 {
		t.Fatalf("expected the Velocity-only jar to be skipped under Paper, got %v", installs)
	}
	if _, err := os.Stat(jarPath); err != nil {
		t.Fatalf("skipped jar should remain in install/: %v", err)
	}

	installs, err = Apply(dir, Velocity, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 1 {
		t.Fatalf("expected 1 install under Velocity, got %d: %v", len(installs), installs)
	}
	dest := filepath.Join(dir, "foo.jar")
	if installs[0].Path != dest {
		t.Fatalf("unexpected install path: %s", installs[0].Path)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("jar should now exist in plugins/: %v", err)
	}
	if _, err := os.Stat(jarPath); !os.IsNotExist(err) {
		t.Fatalf("install-folder source should have been removed, stat err = %v", err)
	}
}

func TestApply_PaperJarInstallsViaPluginYML(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJarWithEntries(t, filepath.Join(installDir, "bar.jar"), "plugin.yml")

	installs, err := Apply(dir, Paper, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 1 {
		t.Fatalf("expected 1 install, got %d: %v", len(installs), installs)
	}
}

func TestApply_PaperJarInstallsViaPaperPluginYMLAlone(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJarWithEntries(t, filepath.Join(installDir, "baz.jar"), "paper-plugin.yml")

	installs, err := Apply(dir, Paper, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 1 {
		t.Fatalf("expected 1 install, got %d: %v", len(installs), installs)
	}
}

func TestApply_JarWithNeitherDescriptorSkippedRestApplies(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJarWithEntries(t, filepath.Join(installDir, "unrelated.jar"), "fabric.mod.json")
	writeJarWithEntries(t, filepath.Join(installDir, "good.jar"), "plugin.yml")

	installs, err := Apply(dir, Paper, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 1 || filepath.Base(installs[0].Path) != "good.jar" {
		t.Fatalf("expected only good.jar to install, got %v", installs)
	}
	if _, err := os.Stat(filepath.Join(installDir, "unrelated.jar")); err != nil {
		t.Fatalf("jar with no matching descriptor should remain in install/: %v", err)
	}
}

func TestApply_FatJarInstallsUnderBothServerTypes(t *testing.T) {
	for _, st := range []ServerType{Velocity, Paper} {
		dir := t.TempDir()
		installDir := filepath.Join(dir, "install")
		if err := os.MkdirAll(installDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeJarWithEntries(t, filepath.Join(installDir, "fat.jar"), "plugin.yml", "velocity-plugin.json")

		installs, err := Apply(dir, st, testLogger())
		if err != nil {
			t.Fatalf("Apply(%s): %v", st, err)
		}
		if len(installs) != 1 {
			t.Fatalf("Apply(%s): expected 1 install for the fat jar, got %d: %v", st, len(installs), installs)
		}
	}
}

func TestApply_DestinationAlreadyExistsSkipped(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJarWithEntries(t, filepath.Join(dir, "foo.jar"), "velocity-plugin.json")
	jarPath := filepath.Join(installDir, "foo.jar")
	writeJarWithEntries(t, jarPath, "velocity-plugin.json")

	installs, err := Apply(dir, Velocity, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 0 {
		t.Fatalf("expected no installs when destination already exists, got %v", installs)
	}
	if _, err := os.Stat(jarPath); err != nil {
		t.Fatalf("source should remain untouched in install/: %v", err)
	}
}

func TestApply_IgnoresNonJarFiles(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "README.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	installs, err := Apply(dir, Velocity, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 0 {
		t.Fatalf("expected no installs, got %v", installs)
	}
}

func TestApply_SkipsCorruptJarsAndAppliesRest(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGarbage(t, filepath.Join(installDir, "corrupt.jar"))
	writeJarWithEntries(t, filepath.Join(installDir, "good.jar"), "velocity-plugin.json")

	installs, err := Apply(dir, Velocity, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 1 || filepath.Base(installs[0].Path) != "good.jar" {
		t.Fatalf("expected the one valid jar to install despite the corrupt one, got %v", installs)
	}
}

func TestApply_PreservesSourceFilePermissions(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jarPath := filepath.Join(installDir, "foo.jar")
	writeJarWithEntries(t, jarPath, "velocity-plugin.json")
	if err := os.Chmod(jarPath, 0o640); err != nil {
		t.Fatal(err)
	}

	installs, err := Apply(dir, Velocity, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 1 {
		t.Fatalf("expected 1 install, got %d: %v", len(installs), installs)
	}

	info, err := os.Stat(installs[0].Path)
	if err != nil {
		t.Fatalf("stat installed jar: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o640 {
		t.Fatalf("expected installed jar to keep the source's 0640 permissions, got %#o", perm)
	}
}

func TestApply_CleanupFailureStillCountsAsInstalled(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits don't block unlink")
	}

	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jarPath := filepath.Join(installDir, "foo.jar")
	writeJarWithEntries(t, jarPath, "velocity-plugin.json")

	// Deny write on installDir so the post-rename os.Remove(jarPath) fails,
	// even though the rename that actually installs the jar (into dir, not
	// installDir) already succeeded.
	if err := os.Chmod(installDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(installDir, 0o755) })

	installs, err := Apply(dir, Velocity, testLogger())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(installs) != 1 {
		t.Fatalf("expected the install to still be recorded despite the cleanup failure, got %d: %v", len(installs), installs)
	}

	dest := filepath.Join(dir, "foo.jar")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("installed jar should exist: %v", err)
	}
	if _, err := os.Stat(jarPath); err != nil {
		t.Fatalf("expected the install-folder source to remain as an uncleaned leftover: %v", err)
	}
}
