package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/typegen"
)

// stageTypes writes app's generated clients into its build snapshot, where the app's own source keeps
// them: TypeScript clients in generated/, Python and Go clients in holon/.
func stageTypes(p *preparedTypes) error {
	if err := writeClients(p, p.root); err != nil {
		return err
	}
	return stabilizeTimes(p.root, p.prepared.Targets(p.name))
}

// writeClients writes p's generated clients into root, laid out as the app's directory.
func writeClients(p *preparedTypes, root string) error {
	for _, target := range p.prepared.Targets(p.name) {
		if err := p.prepared.WriteSlot(p.name, target, root); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
	}
	return nil
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
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func readTypeJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
