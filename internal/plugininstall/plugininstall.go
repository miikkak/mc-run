// Package plugininstall applies mc-run's plugins/install folder: unlike
// plugins/update (see internal/pluginupdate), which only ever version-bumps
// a plugin already present in plugins/, plugins/install has no existing
// counterpart to match against — it exists to get a brand-new plugin jar
// into plugins/ before the JVM starts, with no automated way to do that
// otherwise (miikkak/mc-run#77).
package plugininstall

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ServerType selects which plugin descriptor(s) a jar must declare before
// plugininstall will copy it into plugins/.
type ServerType int

const (
	Velocity ServerType = iota
	Paper
)

// String returns the lowercase name used on the CLI and in log output.
func (t ServerType) String() string {
	switch t {
	case Velocity:
		return "velocity"
	case Paper:
		return "paper"
	default:
		return "unknown"
	}
}

// ParseServerType parses "velocity" or "paper" (case-insensitive) into a
// ServerType.
func ParseServerType(s string) (ServerType, error) {
	switch strings.ToLower(s) {
	case "velocity":
		return Velocity, nil
	case "paper":
		return Paper, nil
	default:
		return 0, fmt.Errorf("plugininstall: unknown server type %q (want %q or %q)", s, "velocity", "paper")
	}
}

// descriptorEntries lists the jar-root zip entry names that indicate a jar
// declares support for the given ServerType. This is a presence check, not
// an exclusivity check: real plugin jars commonly bundle descriptors for
// several platforms in one fat jar (e.g. plugin.yml + velocity-plugin.json),
// picked between at runtime by whichever platform actually loads them, so a
// jar with extra unrelated descriptors is still a legitimate match here.
func descriptorEntries(serverType ServerType) []string {
	switch serverType {
	case Velocity:
		return []string{"velocity-plugin.json"}
	case Paper:
		return []string{"plugin.yml", "paper-plugin.yml"}
	default:
		return nil
	}
}

// Install describes one jar copied from plugins/install into plugins/.
type Install struct {
	Path string // final path in pluginsDir
}

// Apply looks for jars directly in <pluginsDir>/install, copies each one
// that declares a descriptor matching serverType into pluginsDir, and
// removes it from the install folder. A jar with no matching descriptor, an
// unreadable/corrupt jar, or a jar whose destination filename already
// exists in pluginsDir is skipped and logged rather than failing the whole
// call, so one bad or misplaced jar can't block server startup.
func Apply(pluginsDir string, serverType ServerType, logger *slog.Logger) ([]Install, error) {
	if logger == nil {
		logger = slog.Default()
	}

	entries := descriptorEntries(serverType)
	if entries == nil {
		return nil, fmt.Errorf("plugininstall: unknown server type %v", serverType)
	}

	installDir := filepath.Join(pluginsDir, "install")
	switch info, err := os.Stat(installDir); {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil // no install folder: nothing to do, not an error
	case err != nil:
		return nil, fmt.Errorf("plugininstall: stat %s: %w", installDir, err)
	case !info.IsDir():
		return nil, nil // something else named "install" exists: silently ignore, matching pluginupdate's "update" handling
	}

	installJars, err := listJars(installDir)
	if err != nil {
		return nil, fmt.Errorf("plugininstall: list %s: %w", installDir, err)
	}

	var applied []Install
	for _, jarPath := range installJars {
		ok, err := hasZipEntry(jarPath, entries...)
		if err != nil {
			logger.Warn("plugininstall: skipping unreadable jar", "path", jarPath, "error", err)
			continue
		}
		if !ok {
			logger.Warn("plugininstall: skipping jar: no matching descriptor for this server type", "path", jarPath, "server_type", serverType, "want_any_of", entries)
			continue
		}

		dest := filepath.Join(pluginsDir, filepath.Base(jarPath))
		if _, err := os.Stat(dest); err == nil {
			logger.Warn("plugininstall: skipping jar: already exists in plugins/, use plugins/update/ to update an existing plugin", "path", jarPath, "dest", dest)
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			logger.Warn("plugininstall: skipping jar: could not stat destination", "path", jarPath, "dest", dest, "error", err)
			continue
		}

		newPath, err, cleanupErr := install(jarPath, dest)
		if err != nil {
			logger.Warn("plugininstall: failed to install jar", "path", jarPath, "error", err)
			continue
		}
		if cleanupErr != nil {
			// The jar itself is already live in plugins/ — this is a
			// leftover-file warning, not a failure.
			logger.Warn("plugininstall: jar installed but cleanup left a stale file behind", "path", newPath, "error", cleanupErr)
		}

		logger.Info("plugininstall: installed plugin", "path", newPath)
		applied = append(applied, Install{Path: newPath})
	}

	return applied, nil
}

// listJars returns the *.jar regular files directly inside dir (no
// recursion), sorted by filename for deterministic order.
func listJars(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var jars []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jar") {
			continue
		}
		jars = append(jars, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(jars)
	return jars, nil
}

// hasZipEntry reports whether jarPath, opened as a zip, contains any entry
// whose name exactly matches one of names.
func hasZipEntry(jarPath string, names ...string) (bool, error) {
	r, err := zip.OpenReader(jarPath)
	if err != nil {
		return false, fmt.Errorf("plugininstall: open %s: %w", jarPath, err)
	}
	defer func() { _ = r.Close() }()

	for _, f := range r.File {
		for _, name := range names {
			if f.Name == name {
				return true, nil
			}
		}
	}
	return false, nil
}

// install copies srcPath's content into dest, atomically: the content is
// fully written to a temp file in dest's directory before any rename, so a
// failure partway through never leaves a corrupted, partially-written jar
// in plugins/. Once the rename succeeds the install has taken effect — the
// returned newPath already holds the jar's content. Removing srcPath from
// the install folder afterward is best-effort cleanup: a failure there is
// reported via cleanupErr, not err, so callers don't mistake a jar that
// actually installed for one that didn't.
func install(srcPath, dest string) (newPath string, err error, cleanupErr error) {
	dir := filepath.Dir(dest)

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", srcPath, err), nil
	}

	tmpPath, err := writeTempFile(dir, srcPath, srcInfo.Mode().Perm())
	if err != nil {
		return "", fmt.Errorf("stage jar from %s: %w", srcPath, err), nil
	}
	defer func() { _ = os.Remove(tmpPath) }() // no-op once successfully renamed away

	if err := os.Rename(tmpPath, dest); err != nil {
		return "", fmt.Errorf("rename staged jar to %s: %w", dest, err), nil
	}

	if rmErr := os.Remove(srcPath); rmErr != nil {
		return dest, nil, fmt.Errorf("remove %s: %w", srcPath, rmErr)
	}

	return dest, nil, nil
}

// writeTempFile copies src into a new temp file in dir with the given
// permission bits, fsyncs and closes it, and returns its path. The caller
// is responsible for renaming (or removing) it.
func writeTempFile(dir, src string, perm os.FileMode) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(dir, ".plugininstall-*.jar.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	return tmpPath, nil
}
