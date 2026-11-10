package installation

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/holon-sh/protocol"
)

const (
	manifestFileName = "holon.yaml"
	dockerignoreFile = ".dockerignore"
)

type manifestSpec struct {
	Apps     []manifestApp `description:"Applications in this project." json:"apps,omitempty"`
	file     string
	sources  []manifestApp
	access   *protocol.AppAccess
	deployed map[string]*protocol.AppAccess
}

type packageRun struct {
	Transport manifestTransport            `json:"transport"      required:"true"`
	Args      []string                     `json:"args,omitempty"`
	Env       map[string]mcpregistry.Value `json:"env,omitempty"`
}

type manifestTransport struct {
	Type string `description:"stdio or streamable-http."                                       enum:"stdio,streamable-http" json:"type" required:"true"`
	URL  string `description:"For streamable-http, http://localhost:PORT/mcp. Omit for stdio." json:"url,omitempty"`
}

type manifestRemote struct {
	URL string `description:"Remote Streamable HTTP MCP endpoint." format:"uri" json:"url" minLength:"1" pattern:"^https://" required:"true"`
}

type resolvedApp struct {
	protocol.App

	registryPackage *mcpregistry.Package
	types           *preparedTypes
	descriptor      *mcpregistry.Descriptor
	stdio           bool
	catalogEnv      map[string]protocol.Input
}

type manifestApp struct {
	Mount string `description:"Production browser path; defaults to /APP. Use / for the origin root. Development uses app-name paths." json:"mount,omitempty"`

	Remote  *manifestRemote              `json:"remote,omitempty"`
	OAuth   *manifestOAuth               `description:"OAuth client registration and secret configuration."        json:"oauth,omitempty"`
	Headers map[string]mcpregistry.Value `description:"Remote header values may be literals or secret references." json:"headers,omitempty"`

	Access *protocol.AppAccess `description:"Server and tool authorization: server: {} is public, oauth: {} requires sign-in. Development sessions admit only signed-in developers." json:"access" required:"true"`

	Name string `description:"Unique app DNS label." json:"name,omitempty" required:"true"`

	Image string `description:"SHA-256-pinned OCI image." json:"image,omitempty"`

	Port *int `description:"HTTP port for image apps only; defaults to 3000." json:"port,omitempty" maximum:"65535" minimum:"1"`

	Path      string                       `description:"Relative source directory."                                              json:"path,omitempty"`
	Registry  *protocol.RegistrySource     `description:"MCP Registry server whose OCI, npm or PyPI package Holon runs; the package may be inferred. Name a remote endpoint with remote." json:"registry,omitempty"`
	Run       *packageRun                  `description:"Registry runtime arguments; cannot combine with arguments or headers."   json:"run,omitempty"`
	Arguments map[string]mcpregistry.Value `description:"Argument literals, or secrets named for the input (--api-key: API_KEY)." json:"arguments,omitempty"`
	resolved  resolvedApp
}

func (app *manifestApp) UnmarshalJSON(data []byte) error {
	type declaration manifestApp
	return protocol.DecodeJSONNonNull(data, (*declaration)(app))
}

func resolveAppMetadata(app *manifestApp, pkg mcpregistry.Package) error {
	app.resolved.stdio = pkg.Transport.Type == "stdio"
	port, _ := pkg.Port()
	catalog, err := pkg.App(nil, mcpregistry.RunConfig{})
	if err != nil {
		return err
	}
	app.resolved.catalogEnv = catalog.Env
	run := ptr.Deref(app.Run, packageRun{})
	declared, err := pkg.App(app.Arguments, mcpregistry.RunConfig{Args: run.Args, Env: run.Env})
	if err != nil {
		return err
	}
	app.resolved.Env, app.resolved.EnvTemplates, app.resolved.Args = declared.Env, declared.EnvTemplates, declared.Args
	for _, name := range app.resolved.Requires {
		variable := protocol.DependencyEnvName(name)
		if input := app.resolved.Env[variable]; input.IsSecret || input.Value != nil || input.Default != nil {
			return fmt.Errorf("dependency endpoint %s cannot be secret or have a value or default", variable)
		}
		delete(app.resolved.Env, variable)
	}
	if app.resolved.stdio && app.OAuth != nil {
		return errors.New("oauth requires an HTTP MCP server")
	}
	app.resolved.Port = int32(cmp.Or(port, ptr.Deref(app.Port, 0), 3000))
	return nil
}

// parseManifest reads a holon.yaml, naming it in errors by its path from the working directory when it
// is inside it.
func parseManifest(data []byte, sourceName string) (manifestSpec, error) {
	working, _ := os.Getwd()
	if relative, err := filepath.Rel(working, sourceName); err == nil && filepath.IsLocal(relative) {
		sourceName = relative
	}
	document, err := yaml.YAMLToJSONStrict(data)
	if err != nil {
		return manifestSpec{}, fmt.Errorf("%s: %w", sourceName, err)
	}
	if problems := validateProject(document); len(problems) != 0 {
		return manifestSpec{}, fmt.Errorf("%s: %s", sourceName, strings.Join(problems, "\n"+sourceName+": "))
	}
	var p manifestSpec
	if err := json.Unmarshal(document, &p); err != nil {
		return manifestSpec{}, fmt.Errorf("%s: %w", sourceName, err)
	}
	var errs []string
	if len(p.Apps) == 0 {
		errs = append(errs, sourceName+": apps must contain at least one entry")
	}
	names := sets.New[string]()
	for i := range p.Apps {
		label := fmt.Sprintf("%s: apps[%d]", sourceName, i)
		a := &p.Apps[i]
		if a.Port != nil && (a.Image == "" || !protocol.ValidPort(*a.Port)) {
			errs = append(errs, label+".port requires image and must be between 1 and 65535")
		}
		if err := protocol.ValidateAccess(a.Access); err != nil {
			errs = append(errs, label+": "+err.Error())
		}
		sources := 0
		for _, present := range []bool{a.Path != "", a.Image != "", a.Registry != nil, a.Remote != nil} {
			if present {
				sources++
			}
		}
		if sources != 1 {
			errs = append(errs, label+": declare exactly one of path, image, registry, or remote")
		}
		if a.Remote != nil {
			if _, err := protocol.RemoteURL(a.Remote.URL); err != nil {
				errs = append(errs, label+".remote: "+err.Error())
			}
		}
		if a.Image != "" && !protocol.IsValidDigestImage(a.Image) {
			errs = append(errs, label+".image must use an explicit registry and sha256 digest")
		}
		if a.Registry != nil {
			if err := a.Registry.Validate(); err != nil {
				errs = append(errs, label+".registry: "+err.Error())
			}
			if a.Registry.Remote != nil {
				errs = append(errs, label+".registry.remote is no longer supported; name the endpoint with remote: {url: ...}")
			}
		}
		if a.Run != nil {
			if a.Registry == nil || a.Registry.Package == nil || a.Registry.Package.RegistryType != "oci" {
				errs = append(errs, label+".run requires a registry OCI package")
			}
			if len(a.Arguments) != 0 || len(a.Headers) != 0 {
				errs = append(errs, label+".run cannot be combined with arguments or headers")
			}
		}
		if !protocol.ValidAppName(a.Name) {
			errs = append(errs, label+".name must be a non-reserved DNS label of at most 49 characters")
		}
		if a.Name != "" && names.Has(a.Name) {
			errs = append(errs, fmt.Sprintf("%s.name duplicates '%s'", label, a.Name))
		}
		names.Insert(a.Name)
		if filepath.IsAbs(a.Path) {
			errs = append(errs, label+".path must be a relative directory")
		}
	}
	if len(errs) > 0 {
		slices.Sort(errs)
		return manifestSpec{}, errors.New(strings.Join(errs, "\n"))
	}
	return p, nil
}

func (p *manifestSpec) selectApps(args []string) ([]string, error) {
	var names []string
	for _, arg := range args {
		source, err := typegen.ParseSource(arg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", arg, err)
		}
		if source.Alias == "" {
			names = append(names, arg)
			continue
		}
		if slices.ContainsFunc(slices.Concat(p.Apps, p.sources), func(app manifestApp) bool { return app.Name == source.Alias }) {
			return nil, fmt.Errorf("app %s is declared twice", source.Alias)
		}
		app := manifestApp{Name: source.Alias, Path: source.Path}
		switch {
		case source.Registry != nil:
			app.Registry = &protocol.RegistrySource{RegistryRef: *source.Registry}
			err = app.Registry.Validate()
		case source.Endpoint != "":
			app.Remote = &manifestRemote{URL: source.Endpoint}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", arg, err)
		}
		p.sources = append(p.sources, app)
		names = append(names, app.Name)
	}
	if len(args) == 0 && p.file == "" {
		matches, _ := filepath.Glob(filepath.Join("*", "server.json"))
		for _, match := range matches {
			app, err := p.app(filepath.Dir(match))
			if err != nil {
				return nil, err
			}
			p.sources = append(p.sources, app)
		}
		if len(p.sources) == 0 {
			return nil, errors.New("no holon.yaml and no ./*/server.json; name apps as APP=SOURCE")
		}
	}
	for _, name := range names {
		if _, err := p.app(name); err != nil {
			return nil, err
		}
	}
	return names, nil
}

func (p manifestSpec) app(name string) (manifestApp, error) {
	if !protocol.ValidAppName(name) {
		return manifestApp{}, fmt.Errorf("invalid app name %q", name)
	}
	declared := slices.Concat(p.Apps, p.sources)
	if i := slices.IndexFunc(declared, func(app manifestApp) bool { return app.Name == name }); i >= 0 {
		return declared[i], nil
	}
	path, err := filepath.Abs(name)
	if _, statErr := os.Stat(filepath.Join(path, "server.json")); err != nil || statErr != nil {
		return manifestApp{}, fmt.Errorf("unknown app %q", name)
	}
	return manifestApp{Name: name, Path: path}, nil
}
