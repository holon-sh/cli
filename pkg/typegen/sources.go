// Package typegen turns MCP server sources into typed clients written into the projects that call them.
//
// A Consumer is a project directory that receives clients for the servers it names as Sources.
// Sources can be supplied explicitly or read from sh.holon.requires with DeclaredSources.
// The caller chooses which targets receive them; unresolved sources are errors. Connect opens a
// session with a source's running server instead.
package typegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

const dockerignoreFile = ".dockerignore"

// appName constrains aliases and target names: a DNS label usable as apps.<alias>.
var appName = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

// Targets name the language and runtime a consumer receives clients for; slots names where they go.
const (
	TargetTS = "ts"
	TargetUI = "ui"
	TargetPy = "py"
	TargetGo = "go"
)

var targets = []string{TargetTS, TargetUI, TargetPy, TargetGo}

var NodePackages = map[string]string{TargetTS: "holon-sh", TargetUI: "holon-ui"}

// Manifests are the files that declare each target's runtime dependency.
var Manifests = map[string]string{TargetTS: "package.json", TargetUI: "package.json", TargetPy: "pyproject.toml", TargetGo: "go.mod"}

// Source is one MCP server whose contract becomes a client, supplied explicitly or declared in
// sh.holon.requires.
type Source struct {
	// Alias names the client (apps.<alias>). Empty derives it: the last segment of the server's
	// name (example.com/orders → orders), or the live server's serverInfo name for an Endpoint.
	Alias  string
	Server Server
}

// Server is where a contract comes from: a Path, an Endpoint, a Registry or a *Document.
type Server interface{ server() }

// Path is the absolute path of a server entry file, or of a directory holding exactly one.
type Path struct {
	Dir string
	// Served is what its running server answered, when it already has.
	Served *Served
}

// Endpoint is a live MCP endpoint; with an Alias, <ALIAS>_MCP_TOKEN supplies a bearer token for its origin.
type Endpoint string

// Registry is an MCP registry server version, read from its OCI package's document or its streamable-http remote.
type Registry protocol.RegistryRef

func (p Path) String() string      { return p.Dir }
func (r Registry) String() string  { return r.Name + "@" + r.Version }
func (d *Document) String() string { return d.Identity.Name + "@" + d.Identity.Version }
func (Path) server()               {}
func (Endpoint) server()           {}
func (Registry) server()           {}
func (*Document) server()          {}

// NewEndpoint accepts an HTTP(S) MCP URL without credentials or a fragment.
func NewEndpoint(url string) (Endpoint, error) {
	return Endpoint(url), mcpregistry.ValidateEndpoint(url)
}

// NewRegistry accepts a registry server version with a namespaced name and an exact version.
func NewRegistry(ref protocol.RegistryRef) (Registry, error) {
	return Registry(ref), validRegistryRef(ref)
}

var registryPath = regexp.MustCompile(`^/v0(?:\.1)?/servers/([^/]+)/versions/([^/]+)$`)

// ParseSource reads one [ALIAS=]PATH or URL argument. Paths are resolved against the working directory.
func ParseSource(arg string) (Source, error) {
	var source Source
	if alias, rest, ok := strings.Cut(arg, "="); ok && appName.MatchString(alias) {
		source.Alias, arg = alias, rest
	}
	if arg == "" {
		return source, errors.New("a server path or URL is required")
	}
	path, err := filepath.Abs(arg)
	source.Server = Path{Dir: path}
	if u, parseErr := url.Parse(arg); parseErr == nil && (u.Scheme == "http" || u.Scheme == "https") {
		if m := registryPath.FindStringSubmatch(u.EscapedPath()); m != nil {
			name, _ := url.PathUnescape(m[1])
			version, _ := url.PathUnescape(m[2])
			source.Server, err = NewRegistry(protocol.RegistryRef{Name: name, Version: version, Registry: u.Scheme + "://" + u.Host})
		} else {
			source.Server, err = NewEndpoint(arg)
		}
	}
	if len(source.Alias) > 49 {
		err = fmt.Errorf("alias %q must be a DNS label of at most 49 characters", source.Alias)
	}
	return source, err
}

// key identifies a fetched source (a registry version, or an endpoint with the alias that names its
// token), so it resolves once per Prepare; path and document sources never reach it.
func (s Source) key() string {
	if r, ok := s.Server.(Registry); ok {
		return "registry:" + protocol.RegistryRef(r).RegistryURL() + "#" + r.String()
	}
	return "endpoint:" + s.Alias + "=" + string(s.Server.(Endpoint))
}

// Consumer is a project directory that receives typed clients.
type Consumer struct {
	// Name signs the generated files' banners and labels output.
	Name string
	// Dir is the absolute project directory.
	Dir string
	// Server is the consumer's own MCP server, inside Dir, or empty for none. It supplies the
	// consumer's identity, contract (Metadata) and check configuration. Browser clients call it
	// under its derived name; an Alias replaces that name and adds the client to every other target.
	Server Path
	Alias  string
	// Targets are the clients to write.
	// A server can have no targets when only its contract is needed.
	Targets []string
	// Sources are the servers that become apps.<alias> clients.
	Sources []Source
}

func (c Consumer) installed(target string) error {
	switch target {
	case TargetTS, TargetUI:
		return installedRuntime(c.Dir, NodePackages[target])
	case TargetPy:
		if pythonEnvironment(c.Dir) == "" {
			return fmt.Errorf("%s has no Python environment", c.Dir)
		}
	}
	return nil
}

// serverEntry resolves a path source: path is an entry file, or a directory holding exactly one of
// server.py, src/server.py, server.ts, src/server.ts, server.js, src/server.js (and server.go).
// It returns the application root (the directory above src/), the entry relative to it, and its language.
func serverEntry(path string) (root, entry, language string, err error) {
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

var notKey = regexp.MustCompile(`[^a-z0-9-]+`)

// ServerKey is the client name derived from a server name: its last segment, lower-cased, with
// runs of other characters as hyphens, trimmed to a 49-character label.
func ServerKey(serverName string) string {
	key := serverName[strings.LastIndex(serverName, "/")+1:]
	key = strings.Trim(notKey.ReplaceAllString(strings.ToLower(key), "-"), "-")
	return strings.TrimRight(key[:min(len(key), 49)], "-")
}

func readTypeJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func lexicallyInside(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(relative)
}
