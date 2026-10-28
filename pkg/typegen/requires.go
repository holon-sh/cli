package typegen

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
)

// DeclaredSources resolves the descriptor and returns its dependencies, sorted by alias.
func DeclaredSources(ctx context.Context, dir string, options Options) ([]Source, error) {
	path := filepath.Join(dir, "server.json")
	data, err := ReadDescriptor(ctx, dir, options)
	if err == nil && len(data) == 0 {
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
