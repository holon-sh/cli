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

func readTypeJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func sourceEntry(root string) (string, error) {
	entry, err := typegen.SourceEntry(root)
	if err != nil || !slices.Contains([]string{".ts", ".js"}, filepath.Ext(entry)) {
		return entry, err
	}
	var pkg typegen.PackageJSON
	if err := readTypeJSON(filepath.Join(root, "package.json"), &pkg); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if pkg.Launches() {
		return "", nil
	}
	return entry, nil
}
