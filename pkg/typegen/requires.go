package typegen

import (
	"errors"
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

// AppConsumer is the consumer for the app kept in dir, without its dependencies: the slots its SDK
// dependencies declare and its OwnServer. It returns nil for an app with neither, which has nothing
// to generate or check.
func AppConsumer(name, dir string) (*Consumer, error) {
	targets, err := DeclaredTargets(dir)
	if err != nil && !errors.Is(err, ErrNoRuntime) {
		return nil, err
	}
	own, err := OwnServer(dir)
	if err != nil || (len(targets) == 0 && own == nil) {
		return nil, err
	}
	return &Consumer{Name: name, Dir: dir, Targets: targets, Server: own}, nil
}

// DeclaredSources returns one source per sh.holon.requires entry, sorted by alias.
func DeclaredSources(dir string) ([]Source, error) {
	d, err := readDescriptor(dir)
	if err != nil || d == nil {
		return nil, err
	}
	var sources []Source
	for _, alias := range slices.Sorted(maps.Keys(d.Requires)) {
		requirement := d.Requires[alias]
		source := Source{Alias: alias, Endpoint: requirement.URL}
		if requirement.URL == "" {
			source.Registry = (*Reference)(&requirement.RegistryRef)
		}
		sources = append(sources, source)
	}
	return sources, nil
}
