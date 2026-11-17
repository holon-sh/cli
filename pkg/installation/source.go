package installation

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/recipe"
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

func sourceAppStart(app manifestApp, root string) ([]string, error) {
	if _, err := hookartifact.Discover(os.DirFS(root)); err != nil {
		return nil, err
	}
	entry, err := typegen.SourceEntry(root)
	if err != nil {
		return nil, err
	}
	extension := filepath.Ext(entry)
	node := extension == ".ts" || extension == ".js"
	var pkg typegen.PackageJSON
	if node {
		pkg, err = typegen.ReadPackageJSON(root)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if !app.resolved.stdio && (node || extension == ".py") && pkg.Main == "" {
		switch command := pkg.Scripts["start"]; {
		case command == "":
			return recipe.Start(entry), nil
		case command == "node "+entry && entry == filepath.Base(entry):
			return []string{"node", entry}, nil
		}
	}
	return nil, fmt.Errorf("%s: holon dev and holon deploy run an app from a server.py, server.ts or server.js entry; ship it as an image: app", app.Name)
}
