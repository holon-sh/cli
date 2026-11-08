package installation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/holon-sh/cli/pkg/mcpcontract"
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
	selections := map[string]mcpregistry.Requirement{}
	envTypes := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(nodes)) {
		app := nodes[name]
		if err := resolveApp(app); err != nil {
			return manifestSpec{}, fmt.Errorf("%s: %w", name, err)
		}
		for _, dependency := range app.resolved.Requires {
			requirement := app.resolved.descriptor.Requires[dependency]
			if requirement.URL == "" {
				requirement.Registry = requirement.RegistryURL()
			}
			if prior, exists := selections[dependency]; exists && prior != requirement {
				return manifestSpec{}, fmt.Errorf("conflicting references for dependency %s", dependency)
			}
			selections[dependency] = requirement
			if !satisfies(nodes[dependency], requirement) {
				return manifestSpec{}, fmt.Errorf("%s requires %s to be %s; declared app does not match", name, dependency, cmp.Or(requirement.URL, requirement.Name+"@"+requirement.Version))
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

// satisfies reports whether a declared app is the server a requirement names: the remote at its
// URL, or a server whose descriptor has its identity, from its registry when the app names one.
func satisfies(app *manifestApp, requirement mcpregistry.Requirement) bool {
	if requirement.URL != "" {
		return app.Remote != nil && app.Remote.URL == requirement.URL
	}
	d := app.resolved.descriptor
	return d != nil && d.Identity() == mcpcontract.Identity{Name: requirement.Name, Version: requirement.Version} &&
		(app.Registry == nil || app.Registry.RegistryURL() == requirement.Registry)
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
		if app.Arguments != nil {
			return errors.New("remote apps cannot declare arguments")
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
	if d != nil {
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
