package typegen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/holon-sh/cli/pkg/mcpregistry"
)

// serverMention is what an MCP server's entry names: the protocol, an SDK or holon-sh.
var serverMention = regexp.MustCompile(`(?i)mcp|modelcontextprotocol|holon[-_]sh`)

// AppConsumer is the consumer for the app kept in dir, without its dependencies: the slots its SDK
// dependencies declare and its own MCP server, kept in dir or dir/server as the directory holding its
// entry. Only an entry that mentions MCP counts, since a project can keep a plain web server under the
// same file name, and more than one is an error. It returns nil for an app with neither, which has
// nothing to generate or check.
func AppConsumer(name, dir string) (*Consumer, error) {
	targets, err := DeclaredTargets(dir)
	if err != nil {
		return nil, err
	}
	var own Path
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
		if own.Dir != "" {
			return nil, fmt.Errorf("keep exactly one server entry (found them in %s and %s)", own, root)
		}
		own = Path{Dir: root}
	}
	if len(targets) == 0 && own.Dir == "" {
		return nil, nil
	}
	return &Consumer{Name: name, Dir: dir, Targets: targets, Server: own}, nil
}

// Requires returns the dependencies the server.json in dir declares in sh.holon.requires, by alias.
func Requires(dir string) (map[string]mcpregistry.Requirement, error) {
	d, err := readDescriptor(dir)
	if err != nil || d == nil {
		return nil, err
	}
	return d.Requires, nil
}

// RequiredSource is the source an sh.holon.requires entry names: path when given, else its URL or registry version.
func RequiredSource(alias string, r mcpregistry.Requirement, path Path) (Source, error) {
	source := Source{Alias: alias}
	var err error
	switch {
	case path.Dir != "":
		source.Server = path
	case r.URL != "":
		source.Server, err = NewEndpoint(r.URL)
	case r.Name != "":
		source.Server, err = NewRegistry(r.RegistryRef)
	default:
		return source, fmt.Errorf("dependency %s has no server to read: make it a path, remote or registry app in holon.yaml, or give it a published version or url in server.json", alias)
	}
	if !appName.MatchString(alias) {
		err = fmt.Errorf("alias %q must be a DNS label of at most 49 characters", alias)
	}
	return source, err
}
