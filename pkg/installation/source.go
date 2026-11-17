package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

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
	var pkg typegen.PackageJSON
	if slices.Contains([]string{".ts", ".js"}, extension) {
		data, err := os.ReadFile(filepath.Join(root, "package.json"))
		if err == nil {
			err = json.Unmarshal(data, &pkg)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if !app.resolved.stdio && slices.Contains([]string{".py", ".ts", ".js"}, extension) {
		if !pkg.Launches() {
			return recipe.Start(entry), nil
		}
		if pkg.Main == "" && (entry == "server.ts" || entry == "server.js") && pkg.Scripts["start"] == "node "+entry {
			return []string{"node", entry}, nil
		}
	}
	return nil, fmt.Errorf("%s: holon dev and holon deploy run an app from a server.py, server.ts or server.js entry; ship it as an image: app", app.Name)
}
