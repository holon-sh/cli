package installation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

func readManifestDescriptors(ctx context.Context, manifest manifestSpec, apps ...string) (map[string]*manifestApp, error) {
	if len(apps) == 0 {
		for _, app := range slices.Concat(manifest.Apps, manifest.sources) {
			apps = append(apps, app.Name)
		}
	}
	nodes := map[string]*manifestApp{}
	for len(apps) > 0 {
		name := apps[0]
		apps = apps[1:]
		if nodes[name] != nil {
			continue
		}
		app, err := manifest.app(name)
		if err != nil {
			return nil, err
		}
		access := cmp.Or(app.Access, manifest.access, manifest.deployed[name])
		if access == nil {
			return nil, fmt.Errorf("app %s needs an access rule: pass --public or --oauth", name)
		}
		app.resolved = resolvedApp{App: protocol.App{
			Name: app.Name, Mount: app.Mount, Image: app.Image, Access: access,
			OAuth: (*protocol.OAuthClient)(app.OAuth),
		}}
		if app.Path != "" {
			info, statErr := os.Stat(app.Path)
			if statErr != nil {
				return nil, fmt.Errorf("%s path: %w", app.Name, statErr)
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("%s path must be a directory", app.Name)
			}
			app.resolved.descriptor, err = readServerDescriptor(filepath.Join(app.Path, "server.json"))
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		} else if app.Registry != nil {
			app.resolved.descriptor, err = mcpregistry.Fetch(ctx, nil, app.Registry.RegistryRef)
		}
		if err != nil {
			return nil, fmt.Errorf("%s descriptor: %w", app.Name, err)
		}
		if app.resolved.descriptor != nil {
			app.resolved.Icons = app.resolved.descriptor.Icons
			app.resolved.Requires = slices.Sorted(maps.Keys(app.resolved.descriptor.Requires))
		}
		nodes[app.Name] = &app
		apps = append(apps, app.resolved.Requires...)
	}
	return nodes, nil
}

func resolveManifestGraph(ctx context.Context, manifest manifestSpec, apps ...string) (manifestSpec, error) {
	nodes, err := readManifestDescriptors(ctx, manifest, apps...)
	if err != nil {
		return manifestSpec{}, err
	}
	resolved := manifest
	resolved.Apps = nil
	envTypes := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(nodes)) {
		app := nodes[name]
		if err := resolveApp(app); err != nil {
			return manifestSpec{}, fmt.Errorf("%s: %w", name, err)
		}
		declaration := app.resolved.App
		if err := cmp.Or(declaration.OAuth.Validate(declaration.Env), declaration.Egress.Validate(declaration.Env)); err != nil {
			return manifestSpec{}, fmt.Errorf("%s: %w", name, err)
		}
		for variable, input := range declaration.Inputs() {
			kind := input.Kind()
			if cmp.Or(envTypes[variable], kind) != kind {
				return manifestSpec{}, fmt.Errorf("conflicting environment declarations for %s", variable)
			}
			envTypes[variable] = kind
		}
		resolved.Apps = append(resolved.Apps, *app)
	}
	if err := protocol.ValidateAppMounts(resolved.declarations()); err != nil {
		return manifestSpec{}, err
	}
	if problems := protocol.Dependencies(resolved.declarations()); len(problems) != 0 {
		return manifestSpec{}, errors.New(strings.Join(problems, "; "))
	}
	return resolved, nil
}

func resolveApp(app *manifestApp) error {
	if app.Remote != nil {
		if app.Arguments != nil {
			return errors.New("remote apps cannot declare arguments")
		}
		var err error
		app.resolved.Remote, app.resolved.Env, err = remoteApp(app.Remote.URL, app.Headers)
		return err
	}
	if len(app.Headers) != 0 {
		return errors.New("headers require a remote server")
	}
	d := app.resolved.descriptor
	var pkg mcpregistry.Package
	if d != nil && (len(d.Packages) != 0 || app.Registry != nil) {
		selection := protocol.PackageSelection{RegistryType: "oci"}
		if app.Registry != nil {
			selection = ptr.Deref(app.Registry.Package, protocol.PackageSelection{})
		}
		index, err := mcpregistry.SelectPackage(d, selection)
		if err != nil {
			return err
		}
		pkg = d.Packages[index]
		if app.Run != nil {
			pkg = mcpregistry.Package{
				RegistryType: pkg.RegistryType, Identifier: pkg.Identifier,
				Transport: mcpregistry.Transport{Type: app.Run.Transport.Type, URL: app.Run.Transport.URL},
			}
		}
		if err := pkg.ValidateTransport(); err != nil {
			return err
		}
	}
	if app.Registry != nil {
		if pkg.RegistryType == "oci" {
			app.resolved.Image = pkg.Identifier
		} else {
			app.resolved.registryPackage = &pkg
		}
	}
	if d != nil {
		app.resolved.Egress = d.Egress
	}
	return resolveAppMetadata(app, pkg)
}

// remoteApp is a remote endpoint with headers that are literals or secret references, each secret a
// required input of the same name.
func remoteApp(url string, headers map[string]mcpregistry.Value) (*protocol.Remote, map[string]protocol.Input, error) {
	remote := &protocol.Remote{URL: url, Headers: map[string]string{}}
	env := map[string]protocol.Input{}
	for name, value := range headers {
		canonical := http.CanonicalHeaderKey(name)
		if _, exists := remote.Headers[canonical]; exists {
			return nil, nil, fmt.Errorf("duplicate header %s", canonical)
		}
		switch {
		case value.Secret != "":
			env[value.Secret] = protocol.Input{IsSecret: true, IsRequired: true}
			remote.Headers[canonical] = "{" + value.Secret + "}"
		case value.Value != nil && protocol.ValidInputValue(*value.Value):
			remote.Headers[canonical] = *value.Value
		default:
			return nil, nil, fmt.Errorf("header %s must be a secret reference or single-line text without NUL characters", canonical)
		}
	}
	return remote, env, remote.Validate(env)
}

func (p manifestSpec) declarations() []protocol.App {
	apps := make([]protocol.App, 0, len(p.Apps))
	for _, app := range p.Apps {
		apps = append(apps, app.resolved.App)
	}
	return apps
}
