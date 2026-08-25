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
	info, err := os.Stat(updateDir)
	if err != nil || !info.IsDir() {
		return nil, nil // no update folder: nothing to do, not an error
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

		newPath, err := replace(pluginPath, updatePath)
		if err != nil {
			logger.Warn("pluginupdate: failed to apply update", "id", id, "from", updatePath, "to", pluginPath, "error", err)
			continue
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

// replace overwrites pluginPath's content with updatePath's, renames the
// result to updatePath's filename (matching Paper: the plugins/ jar ends up
// named like the update jar, not its old name), then removes updatePath.
func replace(pluginPath, updatePath string) (string, error) {
	if err := copyFile(updatePath, pluginPath); err != nil {
		return "", fmt.Errorf("copy %s to %s: %w", updatePath, pluginPath, err)
	}

	newPath := filepath.Join(filepath.Dir(pluginPath), filepath.Base(updatePath))
	if newPath != pluginPath {
		if err := os.Rename(pluginPath, newPath); err != nil {
			return "", fmt.Errorf("rename %s to %s: %w", pluginPath, newPath, err)
		}
	}

	if err := os.Remove(updatePath); err != nil {
		return "", fmt.Errorf("remove %s: %w", updatePath, err)
	}

	return newPath, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
