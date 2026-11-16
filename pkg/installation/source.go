package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/holon-sh/protocol/hookartifact"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/internal/commandenv"
)

func snapshotSource(app manifestApp, destination string) (string, error) {
	tlsDir, err := commandenv.TLSDirectory()
	if err != nil {
		return "", err
	}
	if clientutil.PathInside(tlsDir, app.Path) {
		return "", fmt.Errorf("build context for %s must not contain Holon's certificate authority directory", app.Name)
	}
	return typegen.SnapshotSource(app.Name, app.Path, destination)
}

func sourceAppEntry(app manifestApp, root string) (string, error) {
	if _, err := hookartifact.Discover(os.DirFS(root)); err != nil {
		return "", err
	}
	entry, err := typegen.SourceEntry(root)
	if err != nil {
		return "", err
	}
	var pkg typegen.PackageJSON
	if slices.Contains([]string{".ts", ".js"}, filepath.Ext(entry)) {
		data, err := os.ReadFile(filepath.Join(root, "package.json"))
		if err == nil {
			err = json.Unmarshal(data, &pkg)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	if app.resolved.stdio || pkg.Launches() || !slices.Contains([]string{".py", ".ts", ".js"}, filepath.Ext(entry)) {
		return "", fmt.Errorf("%s: holon dev and holon deploy run an app from a server.py, server.ts or server.js entry; ship it as an image: app", app.Name)
	}
	return entry, nil
}
