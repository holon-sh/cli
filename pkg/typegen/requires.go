package typegen

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// DeclaredSources returns one registry source per sh.holon.requires entry of dir/server.json,
// keyed by its alias and sorted by it. A directory without server.json, or whose server.json
// declares no requires, has no declared sources and returns nil, nil.
func DeclaredSources(dir string) ([]Source, error) {
	path := filepath.Join(dir, "server.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := ParseDescriptor(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var sources []Source
	for _, alias := range slices.Sorted(maps.Keys(d.Requires)) {
		ref := d.Requires[alias]
		sources = append(sources, Source{Alias: alias, Registry: &ref})
	}
	return sources, nil
}
