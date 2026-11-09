package installation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/tailscale/hujson"
)

// stageTypes writes app's generated clients into its build snapshot, where the app's own source keeps
// them: TypeScript clients in generated/, Python and Go clients in holon/.
func stageTypes(p *preparedTypes) error {
	if err := writeClients(p, p.root); err != nil {
		return err
	}
	return stabilizeTimes(p.root)
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

func railpackConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	config := map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		if data, err = hujson.Standardize(data); err != nil {
			return nil, fmt.Errorf("railpack.json: %w", err)
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("railpack.json: %w", err)
		}
	}
	return config, nil
}

// stabilizeTimes gives the files Holon wrote into the snapshot content-derived mtimes, so BuildKit's
// incremental context sync resends only those that changed.
func stabilizeTimes(root string) error {
	for _, directory := range []string{"generated", "holon"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
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

func objectField(parent map[string]any, name string) map[string]any {
	value, ok := parent[name].(map[string]any)
	if !ok {
		value = map[string]any{}
		parent[name] = value
	}
	return value
}

func readTypeJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func writeTypeJSON(path string, value any) error {
	data, _ := json.MarshalIndent(value, "", "  ")
	return mcpcontract.WriteStable(path, append(data, '\n'), 0o644)
}
