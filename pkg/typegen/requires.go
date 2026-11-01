package typegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// ReadDescriptor returns dir's MCP descriptor: its server.json or, without one, the descriptor its
// Python or TypeScript server implies when it runs (the entry's publishing declaration with the
// serverInfo the server reports). It is nil for a directory with neither.
func ReadDescriptor(ctx context.Context, dir string, options Options) (json.RawMessage, error) {
	data, err := os.ReadFile(filepath.Join(dir, "server.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		return data, err
	}
	entry, err := SourceEntry(dir)
	if err != nil || entry == "" || filepath.Ext(entry) == ".go" {
		return nil, err
	}
	s, err := extractSource(ctx, dir, options.defaults())
	if err != nil {
		return nil, err
	}
	return s.descriptor, nil
}

// DeclaredSources returns one registry source per sh.holon.requires entry of dir's descriptor,
// keyed by its alias and sorted by it. A directory without a descriptor, or whose descriptor
// declares no requires, has no declared sources and returns nil, nil.
func DeclaredSources(ctx context.Context, dir string, options Options) ([]Source, error) {
	data, err := ReadDescriptor(ctx, dir, options)
	if err != nil || data == nil {
		return nil, err
	}
	d, err := ParseDescriptor(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "server.json"), err)
	}
	var sources []Source
	for _, alias := range slices.Sorted(maps.Keys(d.Requires)) {
		ref := d.Requires[alias]
		sources = append(sources, Source{Alias: alias, Registry: &ref})
	}
	return sources, nil
}
