package typegen

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/modfile"
)

// manifestIdentity is the name and version that the manifest of the project's first target with one
// names it by: package.json for Node slots, pyproject.toml's [project] table for Python and go.mod's
// module path, which has no version, for Go. It is zero when no manifest names the project.
func manifestIdentity(dir string, targets []string) (mcpcontract.Identity, error) {
	for _, target := range targets {
		file := filepath.Join(dir, map[string]string{TargetTS: "package.json", TargetUI: "package.json", TargetPy: "pyproject.toml", TargetGo: "go.mod"}[target])
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var identity mcpcontract.Identity
		if err == nil {
			switch target {
			case TargetPy:
				var manifest struct{ Project mcpcontract.Identity }
				err = toml.Unmarshal(data, &manifest)
				identity = manifest.Project
			case TargetGo:
				identity.Name = modfile.ModulePath(data)
			default:
				var manifest packageJSON
				err = json.Unmarshal(data, &manifest)
				identity = mcpcontract.Identity{Name: manifest.Name, Version: manifest.Version}
			}
		}
		if err != nil {
			return mcpcontract.Identity{}, fmt.Errorf("%s: %w", file, err)
		}
		if identity.Name != "" {
			identity.Version = cmp.Or(identity.Version, unversioned)
			return identity, nil
		}
	}
	return mcpcontract.Identity{}, nil
}
