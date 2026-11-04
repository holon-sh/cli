package cloud

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/holon-sh/cli/pkg/typegen"

	"github.com/holon-sh/cli/internal/commandenv"
)

func snapshotSource(app manifestApp, destination string) (string, error) {
	tlsDir, err := commandenv.TLSDirectory()
	if err != nil {
		return "", err
	}
	if pathInsideContext(tlsDir, app.Path) {
		return "", fmt.Errorf("build context for %s must not contain Holon's certificate authority directory", app.Name)
	}
	return typegen.SnapshotSource(app.Name, app.Path, destination)
}

func sourceEntry(root string) (string, error) {
	entry, err := typegen.SourceEntry(root)
	if err != nil || !slices.Contains([]string{".ts", ".js"}, filepath.Ext(entry)) {
		return entry, err
	}
	var metadata struct {
		Scripts map[string]string `json:"scripts"`
		Main    string            `json:"main"`
	}
	if err := readTypeJSON(filepath.Join(root, "package.json"), &metadata); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if metadata.Scripts["start"] != "" || metadata.Main != "" {
		return "", nil
	}
	return entry, nil
}
