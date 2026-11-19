package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/holon-sh/protocol/hookartifact"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/pkg/recipe"
	"github.com/holon-sh/cli/pkg/typegen"
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

type sourceApp struct {
	app    manifestApp
	entry  string
	recipe recipe.Recipe
}

func readSource(app manifestApp, root string, output *progressOutput) (sourceApp, error) {
	entry, err := sourceAppEntry(app, root)
	if err != nil {
		return sourceApp{}, err
	}
	steps, err := recipe.Read(os.DirFS(root))
	if err != nil {
		return sourceApp{}, fmt.Errorf("%s: %w; ship it as an image: app", app.Name, err)
	}
	row := app.Name + " Node.js version"
	if notice := steps.NodeVersionNotice; notice != "" {
		output.status(row, notice+" Pin an exact version in .nvmrc for repeatable builds.", progressIdle)
	} else {
		output.clear(row)
	}
	return sourceApp{app: app, entry: entry, recipe: steps}, nil
}
