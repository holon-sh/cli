package installation

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/typegen"
)

// stageTypes writes app's generated clients into its build snapshot, where the app's own source keeps
// them: TypeScript clients in generated/, Python and Go clients in holon/.
func stageTypes(p *preparedTypes) error {
	if err := p.prepared.WriteClients(p.name, p.root); err != nil {
		return err
	}
	return stabilizeTimes(p.root, p.prepared.Targets(p.name))
}

// stabilizeTimes gives the files Holon wrote into the snapshot content-derived mtimes, so BuildKit's
// incremental context sync resends only those that changed.
func stabilizeTimes(root string, targets []string) error {
	for _, target := range targets {
		err := filepath.WalkDir(typegen.SlotDirectory(root, target), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.Chtimes(path, mcpcontract.StableTime(data), mcpcontract.StableTime(data))
		})
		if err != nil {
			return err
		}
	}
	return nil
}
