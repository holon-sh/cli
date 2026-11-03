package cloud

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/typegen"

	"github.com/holon-sh/cli/pkg/commandenv"
)

func validateBuildContext(app manifestApp) error {
	tlsDir, err := commandenv.TLSDirectory()
	if err != nil {
		return err
	}
	if pathInsideContext(tlsDir, app.Path) {
		return fmt.Errorf("build context for %s must not contain Holon's certificate authority directory", app.Name)
	}
	info, err := os.Lstat(app.Path)
	if err != nil {
		return fmt.Errorf("build context for %s: %w", app.Name, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("build context for %s must be a real directory", app.Name)
	}
	return nil
}

func snapshotSource(app manifestApp, destination string) (string, error) {
	if err := validateBuildContext(app); err != nil {
		return "", err
	}
	return typegen.SnapshotSource(app.Name, app.Path, destination)
}

func sourceEntry(root string) (string, error) {
	var metadata struct {
		Scripts map[string]string `json:"scripts"`
		Main    string            `json:"main"`
	}
	if err := readTypeJSON(filepath.Join(root, "package.json"), &metadata); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	entries := []string{"server.py", "src/server.py", "server.go"}
	if metadata.Scripts["start"] == "" && metadata.Main == "" {
		entries = append(entries, "server.ts", "src/server.ts", "server.js", "src/server.js")
	}
	return serverEntry(root, entries)
}

// serverEntry selects one regular server file from the entrypoints supported by its caller.
func serverEntry(root string, entries []string) (string, error) {
	var entry string
	for _, name := range entries {
		if err := regularNoSymlink(filepath.Join(root, name), "application entrypoint"); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return "", err
		}
		if entry != "" {
			return "", errors.New("keep exactly one server entrypoint: server.go at the root, or server.ts, server.js, server.py at the root or in src")
		}
		entry = name
	}
	return entry, nil
}
