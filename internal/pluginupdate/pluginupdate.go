// Package pluginupdate replays PaperMC's plugins/update mechanism for
// Velocity, which has no equivalent of its own (PaperMC/Velocity#809).
// Plugins are matched by the "id" declared in velocity-plugin.json inside
// each jar, not by filename — mirroring the modern Paper implementation
// (io.papermc.paper.plugin.provider.source.FileProviderSource#checkUpdate)
// rather than legacy CraftBukkit's filename-based match.
package pluginupdate

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const descriptorEntry = "velocity-plugin.json"

var errNoDescriptor = errors.New("pluginupdate: no velocity-plugin.json entry")

// descriptor is the subset of velocity-plugin.json mc-run needs.
type descriptor struct {
	ID string `json:"id"`
}

// Update describes one plugin jar that was replaced from the update folder.
type Update struct {
	ID      string // declared Velocity plugin id
	OldPath string // path of the plugins/ jar before the update
	NewPath string // path of the plugins/ jar after the update (renamed to the update jar's filename)
}

// Apply looks for jars in <pluginsDir>/update whose declared plugin id
// matches a jar already in pluginsDir, replaces the old jar's content with
// the update, renames it to the update jar's filename, and removes the
// update-folder source — the same sequence Paper runs for its own
// plugins/update folder. A single unreadable or non-matching jar is skipped
// and logged rather than failing the whole call, so one broken plugin can't
// block server startup.
func Apply(pluginsDir string, logger *slog.Logger) ([]Update, error) {
	if logger == nil {
		logger = slog.Default()
	}

	updateDir := filepath.Join(pluginsDir, "update")
	switch info, err := os.Stat(updateDir); {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil // no update folder: nothing to do, not an error
	case err != nil:
		return nil, fmt.Errorf("pluginupdate: stat %s: %w", updateDir, err)
	case !info.IsDir():
		return nil, nil // something else named "update" exists: silently ignore, matching Paper
	}

	updateJars, err := listJars(updateDir)
	if err != nil {
		return nil, fmt.Errorf("pluginupdate: list %s: %w", updateDir, err)
	}
	if len(updateJars) == 0 {
		return nil, nil
	}

	pluginJars, err := listJars(pluginsDir)
	if err != nil {
		return nil, fmt.Errorf("pluginupdate: list %s: %w", pluginsDir, err)
	}

	// Index update jars by declared plugin id. Sorted filename order makes
	// the "first wins" choice deterministic if two update jars somehow
	// declare the same id.
	byID := make(map[string]string, len(updateJars))
	for _, path := range updateJars {
		id, err := readPluginID(path)
		if err != nil {
			logger.Warn("pluginupdate: skipping unreadable update jar", "path", path, "error", err)
			continue
		}
		if _, exists := byID[id]; exists {
			continue
		}
		byID[id] = path
	}

	var applied []Update
	for _, pluginPath := range pluginJars {
		id, err := readPluginID(pluginPath)
		if err != nil {
			continue // not a Velocity plugin jar (or unreadable) — nothing to match against
		}

		updatePath, ok := byID[id]
		if !ok {
			continue
		}

		newPath, err, cleanupErr := replace(pluginPath, updatePath)
		if err != nil {
			logger.Warn("pluginupdate: failed to apply update", "id", id, "from", updatePath, "to", pluginPath, "error", err)
			continue
		}
		if cleanupErr != nil {
			// The update itself already took effect (newPath holds the new
			// content) — this is a leftover-file warning, not a failure.
			logger.Warn("pluginupdate: update applied but cleanup left stale files behind", "id", id, "path", newPath, "error", cleanupErr)
		}

		logger.Info("pluginupdate: updated plugin", "id", id, "old", filepath.Base(pluginPath), "new", filepath.Base(newPath))
		applied = append(applied, Update{ID: id, OldPath: pluginPath, NewPath: newPath})
	}

	return applied, nil
}

// listJars returns the *.jar regular files directly inside dir (no
// recursion), sorted by filename for deterministic match order.
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

// readPluginID opens jarPath as a zip and decodes the "id" field out of its
// velocity-plugin.json entry.
func readPluginID(jarPath string) (string, error) {
	r, err := zip.OpenReader(jarPath)
	if err != nil {
		return "", fmt.Errorf("pluginupdate: open %s: %w", jarPath, err)
	}
	defer func() { _ = r.Close() }()

	for _, f := range r.File {
		if f.Name != descriptorEntry {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("pluginupdate: open %s in %s: %w", descriptorEntry, jarPath, err)
		}
		defer func() { _ = rc.Close() }()

		var d descriptor
		if err := json.NewDecoder(rc).Decode(&d); err != nil {
			return "", fmt.Errorf("pluginupdate: decode %s in %s: %w", descriptorEntry, jarPath, err)
		}
		if d.ID == "" {
			return "", fmt.Errorf("pluginupdate: %s in %s has no id", descriptorEntry, jarPath)
		}
		return d.ID, nil
	}

	return "", fmt.Errorf("%w: %s", errNoDescriptor, jarPath)
}

// replace atomically installs updatePath's content in place of pluginPath,
// under a name matching updatePath's filename (matching Paper: the
// plugins/ jar ends up named like the update jar, not its old name), then
// removes updatePath. The update jar's content is fully written to a temp
// file — inheriting pluginPath's existing permission bits — before any
// rename touches pluginPath or the final name, so a failure partway through
// (e.g. disk full) never leaves a corrupted, partially-written jar in
// plugins/: pluginPath is untouched until the new content is confirmed on
// disk.
//
// Once the rename succeeds the update has taken effect — the returned
// newPath already holds the new content. Everything after that point
// (removing the stale old-name jar, removing the update-folder source) is
// best-effort cleanup: a failure there is reported via cleanupErr, not err,
// so callers don't mistake an update that actually applied for one that
// didn't.
func replace(pluginPath, updatePath string) (newPath string, err error, cleanupErr error) {
	dir := filepath.Dir(pluginPath)

	oldInfo, err := os.Stat(pluginPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", pluginPath, err), nil
	}

	tmpPath, err := writeTempFile(dir, updatePath, oldInfo.Mode().Perm())
	if err != nil {
		return "", fmt.Errorf("stage update from %s: %w", updatePath, err), nil
	}
	defer func() { _ = os.Remove(tmpPath) }() // no-op once successfully renamed away

	newPath = filepath.Join(dir, filepath.Base(updatePath))
	if err := os.Rename(tmpPath, newPath); err != nil {
		return "", fmt.Errorf("rename staged update to %s: %w", newPath, err), nil
	}

	var cleanupFailures []string
	if newPath != pluginPath {
		if rmErr := os.Remove(pluginPath); rmErr != nil && !os.IsNotExist(rmErr) {
			cleanupFailures = append(cleanupFailures, fmt.Sprintf("remove old jar %s: %v", pluginPath, rmErr))
		}
	}
	if rmErr := os.Remove(updatePath); rmErr != nil {
		cleanupFailures = append(cleanupFailures, fmt.Sprintf("remove %s: %v", updatePath, rmErr))
	}
	if len(cleanupFailures) > 0 {
		cleanupErr = errors.New(strings.Join(cleanupFailures, "; "))
	}

	return newPath, nil, cleanupErr
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

	tmp, err := os.CreateTemp(dir, ".pluginupdate-*.jar.tmp")
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
