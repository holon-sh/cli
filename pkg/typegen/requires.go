package typegen

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// serverMention is what an MCP server's entry names: the protocol, an SDK or holon-sh.
var serverMention = regexp.MustCompile(`(?i)mcp|modelcontextprotocol|holon[-_]sh`)

// OwnServer finds the MCP server a project keeps in dir or dir/server, as the directory holding its
// entry, or returns nil when there is none. Only an entry that mentions MCP counts, since a project
// can keep a plain web server under the same file name. More than one is an error.
func OwnServer(dir string) (*Source, error) {
	var own *Source
	for _, root := range []string{dir, filepath.Join(dir, "server")} {
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		entry, err := SourceEntry(root)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", root, err)
		}
		if entry == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry))
		if err != nil {
			return nil, err
		}
		if !serverMention.Match(data) {
			continue
		}
		if own != nil {
			return nil, fmt.Errorf("keep exactly one server entry (found them in %s and %s)", own.Path, root)
		}
		own = &Source{Path: root}
	}
	return own, nil
}

// DeclaredSources returns one registry source per sh.holon.requires entry, sorted by alias.
func DeclaredSources(dir string) ([]Source, error) {
	d, err := readDescriptor(dir)
	if err != nil || d == nil {
		return nil, err
	}
	var sources []Source
	for _, alias := range slices.Sorted(maps.Keys(d.Requires)) {
		ref := d.Requires[alias]
		sources = append(sources, Source{Alias: alias, Registry: &ref})
	}
	return sources, nil
}
