package cloud

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/protocol"
)

const manifestFileName = "holon.yaml"

type manifestSpec struct {
	Apps     []manifestApp `description:"Applications in this project." json:"apps,omitempty"`
	file     string
	retained map[string]protocol.App
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
	descriptor      *mcpregistry.Descriptor
	stdio           bool
	catalogEnv      map[string]protocol.Input
}

type manifestApp struct {
	Mount string `description:"Production browser path; defaults to /APP. Use / for the origin root. Development uses app-name paths." json:"mount,omitempty"`

	Remote  *manifestRemote              `json:"remote,omitempty"`
	OAuth   *manifestOAuth               `description:"OAuth client registration and secret configuration."        json:"oauth,omitempty"`
	Headers map[string]mcpregistry.Value `description:"Remote header values may be literals or secret references." json:"headers,omitempty"`
	Access  *protocol.AppAccess          `description:"Server and tool authorization; server: {} is public."       json:"access"            required:"true"`

	Name string `description:"Unique app DNS label." json:"name,omitempty" required:"true"`

	Image string `description:"SHA-256-pinned OCI image." json:"image,omitempty"`

	Port *int `description:"HTTP port for image apps only; defaults to 3000." json:"port,omitempty" maximum:"65535" minimum:"1"`

	Registry  *protocol.RegistrySource     `description:"Registry server reference; package or remote selection may be inferred." json:"registry,omitempty"`
	Run       *packageRun                  `description:"Registry runtime arguments; cannot combine with arguments or headers."   json:"run,omitempty"`
	Arguments map[string]mcpregistry.Value `description:"Argument literals, or secrets named for the input (--api-key: API_KEY)." json:"arguments,omitempty"`
	Egress    protocol.Egress              `json:"egress,omitzero"`
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

func parseManifest(data []byte, sourceName string) (manifestSpec, error) {
	var p manifestSpec
	if err := yaml.UnmarshalStrict(data, &p); err != nil {
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
		for _, present := range []bool{a.Image != "", a.Registry != nil, a.Remote != nil} {
			if present {
				sources++
			}
		}
		if sources != 1 {
			errs = append(errs, label+": declare exactly one of image, registry, or remote")
		}
		if a.Remote != nil {
			if err := protocol.ValidateRemoteURL(a.Remote.URL); err != nil {
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
	}
	if len(errs) > 0 {
		slices.Sort(errs)
		return manifestSpec{}, errors.New(strings.Join(errs, "\n"))
	}
	return p, nil
}
