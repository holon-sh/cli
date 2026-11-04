package installation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

func readManifestDescriptors(ctx context.Context, manifest manifestSpec, apps ...string) (map[string]*manifestApp, error) {
	declarations := map[string]manifestApp{}
	selected := len(apps) > 0
	for _, app := range manifest.Apps {
		declarations[app.Name] = app
		if !selected {
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
		if app, ok := manifest.retained[name]; ok {
			nodes[name] = &manifestApp{Name: name, resolved: resolvedApp{App: app}}
			apps = append(apps, app.Requires...)
			continue
		}
		app, exists := declarations[name]
		if !exists {
			return nil, fmt.Errorf("dependency %s must be declared in apps with explicit access.server", name)
		}
		app.resolved = resolvedApp{App: protocol.App{
			Name: app.Name, Mount: app.Mount, Image: app.Image, Egress: app.Egress, Access: app.Access,
			OAuth: (*protocol.OAuthClient)(app.OAuth),
		}}
		var err error
		if app.Registry != nil {
			app.resolved.descriptor, err = mcpregistry.Fetch(ctx, app.Registry.RegistryRef)
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
	selections := map[string]protocol.RegistryRef{}
	envTypes := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(nodes)) {
		app := nodes[name]
		if _, ok := manifest.retained[name]; ok {
			resolved.Apps = append(resolved.Apps, *app)
			continue
		}
		if err := resolveApp(app); err != nil {
			return manifestSpec{}, fmt.Errorf("%s: %w", name, err)
		}
		for _, dependency := range app.resolved.Requires {
			ref := app.resolved.descriptor.Requires[dependency]
			ref.Registry = ref.RegistryURL()
			if prior, exists := selections[dependency]; exists && prior != ref {
				return manifestSpec{}, fmt.Errorf("conflicting references for dependency %s", dependency)
			}
			selections[dependency] = ref
			if _, ok := manifest.retained[dependency]; ok {
				continue
			}
			selected := nodes[dependency]
			if selected.resolved.descriptor == nil || selected.resolved.descriptor.Identity() != (mcpcontract.Identity{Name: ref.Name, Version: ref.Version}) {
				return manifestSpec{}, fmt.Errorf("%s requires %s to be %s@%s; declared app does not match", name, dependency, ref.Name, ref.Version)
			}
			if selected.Registry != nil && selected.Registry.RegistryURL() != ref.Registry {
				return manifestSpec{}, fmt.Errorf("conflicting registries for dependency %s", dependency)
			}
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
	d := app.resolved.descriptor
	var pkg mcpregistry.Package
	var remote *mcpregistry.Transport
	if d != nil && (len(d.Packages) != 0 || app.Registry != nil) {
		selection := ptr.Deref(app.Registry, protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "oci"}})
		index, selected, err := mcpregistry.SelectServerSource(d, selection)
		if err != nil {
			return err
		}
		remote = selected
		if index >= 0 {
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
	}
	if app.Remote != nil {
		remote = &mcpregistry.Transport{URL: app.Remote.URL}
		for name, value := range app.Headers {
			remote.Headers = append(remote.Headers, mcpregistry.DescriptorInput{Name: name, InputFields: mcpregistry.InputFields{IsSecret: value.Secret != "", IsRequired: true}})
		}
	}
	if remote != nil {
		if app.Arguments != nil || app.Egress != nil {
			return errors.New("remote apps cannot declare run, arguments, or egress")
		}
		var err error
		app.resolved.Remote, app.resolved.Env, err = remote.Remote(app.Headers)
		return err
	}
	if len(app.Headers) != 0 {
		return errors.New("headers require a remote server")
	}
	if app.Registry != nil {
		if pkg.RegistryType == "oci" {
			app.resolved.Image = pkg.Identifier
		} else {
			app.resolved.registryPackage = &pkg
		}
	}
	if d != nil && app.Egress == nil {
		app.resolved.Egress = d.Egress
	}
	return resolveAppMetadata(app, pkg)
}

func (p manifestSpec) declarations() []protocol.App {
	apps := make([]protocol.App, 0, len(p.Apps))
	for _, app := range p.Apps {
		apps = append(apps, app.resolved.App)
	}
	return apps
}
