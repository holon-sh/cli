// Package typegen turns MCP server sources into typed client slots.
//
// A Consumer is a project directory that receives clients for the servers it names as Sources.
// Sources can be supplied explicitly or read from sh.holon.requires with DeclaredSources.
// The caller chooses which SDK slots receive them; unresolved sources are errors.
package typegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

const dockerignoreFile = ".dockerignore"

// appName constrains aliases and target names: a DNS label usable as apps.<alias>.
var appName = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

// Targets name the SDK slot a consumer receives clients in.
const (
	TargetTS = "ts" // node_modules/holon-sh/generated, resolved upward from the consumer
	TargetPy = "py" // holon/apps/__init__.py in the consumer
	TargetGo = "go" // holon/ Go package in the consumer; needs Options.GoModule
)

var targets = []string{TargetTS, TargetPy, TargetGo}

// nodePackage is the installed package that owns the ts target's generated slot.
const nodePackage = "holon-sh"

// Source is one MCP server whose contract becomes a client, supplied explicitly or declared in
// sh.holon.requires. Exactly one of Path, Registry, Endpoint, Image, Contract or Document is set.
type Source struct {
	// Alias names the client (apps.<alias>). Empty derives it: the last segment of the server's
	// name (example.com/orders → orders), or the live server's serverInfo name for an Endpoint.
	Alias string
	// Path is a server entry file (server.py, server.ts, server.js, or the same under src/) or a
	// directory containing exactly one. Its contract is extracted from source; server.json beside
	// the entry supplies the identity.
	Path string
	// Registry is an MCP registry server version. Resolve its OCI package's document,
	// falling back to its streamable-http remote when the image has no contract.
	Registry *Reference
	// Endpoint is a live MCP endpoint; initialize and tools/list supply identity and contract.
	Endpoint string
	// Image is an OCI image carrying a contract artifact (Options.Credential authenticates).
	Image string
	// Contract is a newly observed contract to serialize. Use Document for fetched blobs.
	Contract *mcpcontract.Metadata
	// Document retains the exact bytes of an already fetched contract blob.
	Document *Document
}

// key identifies a fetched source (registry, endpoint or image) independently of its alias, so
// it resolves once per Prepare; path and contract sources never reach it.
func (s Source) key() string {
	switch {
	case s.Registry != nil:
		return "registry:" + s.Registry.RegistryURL() + "#" + s.Registry.Name + "@" + s.Registry.Version
	case s.Endpoint != "":
		return "endpoint:" + s.Endpoint
	}
	return "image:" + s.Image
}

func (s Source) String() string {
	switch {
	case s.Path != "":
		return s.Path
	case s.Registry != nil:
		return s.Registry.Name + "@" + s.Registry.Version
	case s.Endpoint != "":
		return s.Endpoint
	case s.Image != "":
		return s.Image
	case s.Document != nil:
		return s.Document.Identity.Name + "@" + s.Document.Identity.Version
	case s.Contract != nil:
		return s.Contract.Identity.Name + "@" + s.Contract.Identity.Version
	}
	return ""
}

func (s Source) validate() error {
	set := 0
	for _, present := range []bool{s.Path != "", s.Registry != nil, s.Endpoint != "", s.Image != "", s.Contract != nil, s.Document != nil} {
		if present {
			set++
		}
	}
	if set != 1 {
		return errors.New("a source is exactly one of a path, a registry version, an endpoint, an image or a contract")
	}
	if s.Alias != "" && (len(s.Alias) > 49 || !appName.MatchString(s.Alias)) {
		return fmt.Errorf("alias %q must be a DNS label of at most 49 characters", s.Alias)
	}
	if s.Registry != nil {
		return s.Registry.Validate()
	}
	return nil
}

// Consumer is a project directory that receives typed clients.
type Consumer struct {
	// Name owns the written slots (holon-generated.json, the Python banner) and labels output.
	Name string
	// Dir is the absolute project directory.
	Dir string
	// Server is the consumer's own MCP server, a path source inside Dir. It supplies the
	// consumer's identity, contract (Metadata) and check configuration.
	Server *Source
	// Targets are the slots to write, from TargetTS, TargetPy and TargetGo.
	Targets []string
	// Sources are the servers that become apps.<alias> clients.
	Sources []Source
}

func (c Consumer) validate() error {
	if c.Name == "" {
		return errors.New("consumer name is required")
	}
	if !filepath.IsAbs(c.Dir) {
		return fmt.Errorf("%s: directory must be absolute", c.Name)
	}
	if info, err := os.Stat(c.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("%s: %s is not a directory", c.Name, c.Dir)
	}
	if len(c.Targets) == 0 {
		return fmt.Errorf("%s: at least one target is required", c.Name)
	}
	for i, target := range c.Targets {
		if !slices.Contains(targets, target) {
			return fmt.Errorf("%s: unknown target %q (expected one of %s)", c.Name, target, strings.Join(targets, ", "))
		}
		if slices.Contains(c.Targets[:i], target) {
			return fmt.Errorf("%s: target %s listed twice", c.Name, target)
		}
	}
	if c.Server != nil {
		if err := c.Server.validate(); err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		if c.Server.Path == "" {
			return fmt.Errorf("%s: the own server must be a path source", c.Name)
		}
	}
	for _, source := range c.Sources {
		if err := source.validate(); err != nil {
			return fmt.Errorf("%s: %s: %w", c.Name, source, err)
		}
	}
	return nil
}

// serverEntry resolves a path source: path is an entry file, or a directory holding exactly one of
// server.py, src/server.py, server.ts, src/server.ts, server.js, src/server.js (and server.go).
// It returns the application root (the directory above src/), the entry relative to it, and its language.
func serverEntry(path string) (root, entry, language string, err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", "", err
	}
	if info.IsDir() {
		root = path
		if entry, err = SourceEntry(root); err != nil {
			return "", "", "", fmt.Errorf("%s: %w", path, err)
		}
		if entry == "" {
			return "", "", "", fmt.Errorf("%s has no server entry: expected one of %s", path, strings.Join(entryFiles, ", "))
		}
	} else {
		if !info.Mode().IsRegular() {
			return "", "", "", fmt.Errorf("server entry must be a regular file: %s", path)
		}
		root = filepath.Dir(path)
		entry = filepath.Base(path)
		if filepath.Base(root) == "src" {
			root = filepath.Dir(root)
			entry = "src/" + entry
		}
		if !slices.Contains(entryFiles, entry) {
			return "", "", "", fmt.Errorf("%s is not a server entry: expected one of %s", path, strings.Join(entryFiles, ", "))
		}
	}
	switch filepath.Ext(entry) {
	case ".py":
		language = "python"
	case ".ts", ".js":
		language = "typescript"
	case ".go":
		language = "go"
	}
	return root, filepath.ToSlash(entry), language, nil
}

// serverKey is the client name derived from a server name: its last segment, lower-cased, with
// runs of other characters as hyphens, trimmed to a 49-character label.
func serverKey(serverName string) string {
	key := serverName[strings.LastIndex(serverName, "/")+1:]
	key = strings.Trim(regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(strings.ToLower(key), "-"), "-")
	return strings.TrimRight(key[:min(len(key), 49)], "-")
}

func regularNoSymlink(path, kind string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file: %s", kind, path)
	}
	return nil
}
func readTypeJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func pathInsideContext(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(relative)
}
