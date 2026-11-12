package installation

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/typegen"
)

func stageTypes(p *preparedTypes) error {
	if err := p.prepared.WriteClients(p.name, p.root); err != nil {
		return err
	}
	for _, target := range p.prepared.Targets(p.name) {
		err := filepath.WalkDir(typegen.SlotDirectory(p.root, target), func(path string, entry fs.DirEntry, err error) error {
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
