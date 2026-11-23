package installation

import (
	"cmp"
	"context"
	"encoding/json"
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

func resolveManifestGraph(ctx context.Context, manifest manifestSpec, apps ...string) (manifestSpec, error) {
	if len(apps) == 0 {
		for _, app := range slices.Concat(manifest.Apps, manifest.sources) {
			apps = append(apps, app.Name)
		}
	}
	resolved := manifest
	resolved.Apps = nil
	for len(apps) > 0 {
		name := apps[0]
		apps = apps[1:]
		if slices.ContainsFunc(resolved.Apps, func(app manifestApp) bool { return app.Name == name }) {
			continue
		}
		app, err := manifest.app(name)
		if err != nil {
			if slices.ContainsFunc(resolved.Apps, func(a manifestApp) bool { return slices.Contains(a.resolved.Requires, name) }) {
				continue
			}
			return manifestSpec{}, err
		}
		access := cmp.Or(app.Access, manifest.access, manifest.deployed[name].Access)
		if access == nil {
			return manifestSpec{}, fmt.Errorf("app %s needs an access rule: pass --public or --oauth", name)
		}
		app.resolved = resolvedApp{App: protocol.App{
			Name: app.Name, Mount: app.Mount, Image: app.Image, Access: access,
			OAuth: app.OAuth.client(),
		}}
		if app.Path != "" {
			info, statErr := os.Stat(app.Path)
			if statErr != nil {
				return manifestSpec{}, fmt.Errorf("%s path: %w", app.Name, statErr)
			}
			if !info.IsDir() {
				return manifestSpec{}, fmt.Errorf("%s path must be a directory", app.Name)
			}
			app.resolved.descriptor, err = readServerDescriptor(filepath.Join(app.Path, "server.json"))
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		} else if app.Registry != nil {
			app.resolved.descriptor, err = mcpregistry.Fetch(ctx, nil, app.Registry.RegistryRef)
		}
		if err != nil {
			return manifestSpec{}, fmt.Errorf("%s descriptor: %w", app.Name, err)
		}
		if app.resolved.descriptor != nil {
			app.resolved.Icons, app.resolved.Egress = app.resolved.descriptor.Icons, app.resolved.descriptor.Egress
			app.resolved.Requires = slices.Sorted(maps.Keys(app.resolved.descriptor.Requires))
		}
		if err := cmp.Or(resolveApp(&app), app.resolved.OAuth.Validate(app.resolved.Env), app.resolved.Egress.Validate(app.resolved.Env)); err != nil {
			return manifestSpec{}, fmt.Errorf("%s: %w", name, err)
		}
		resolved.Apps = append(resolved.Apps, app)
		apps = append(apps, app.resolved.Requires...)
	}
	slices.SortFunc(resolved.Apps, func(a, b manifestApp) int { return strings.Compare(a.Name, b.Name) })
	declared := resolved.declarations()
	if err := protocol.ValidateAppMounts(declared); err != nil {
		return manifestSpec{}, err
	}
	installed := resolved.installed()
	for _, app := range resolved.Apps {
		for _, name := range app.resolved.Requires {
			if slices.Contains(installed, name) && app.resolved.descriptor.Requires[name] != (mcpregistry.Requirement{}) {
				return manifestSpec{}, undeclaredDependency(&app, name)
			}
		}
	}
	for _, name := range installed {
		declared = append(declared, protocol.App{Name: name})
	}
	if problems := protocol.Dependencies(declared); len(problems) != 0 {
		return manifestSpec{}, errors.New(strings.Join(problems, "; "))
	}
	return resolved, nil
}

func (p manifestSpec) installed() []string {
	var names []string
	for _, app := range p.Apps {
		for _, name := range app.resolved.Requires {
			if !slices.Contains(names, name) && !slices.ContainsFunc(p.Apps, func(declared manifestApp) bool { return declared.Name == name }) {
				names = append(names, name)
			}
		}
	}
	return names
}

func undeclaredDependency(requirer *manifestApp, name string) error {
	pin := requirer.resolved.descriptor.Requires[name]
	missing := fmt.Errorf("%s requires %s, which is not an app in this project; declare it in holon.yaml", requirer.Name, name)
	source, value := "registry", any(pin.RegistryRef)
	if pin.URL != "" {
		source, value = "remote", map[string]string{"url": pin.URL}
	}
	data, _ := json.Marshal(value)
	return fmt.Errorf("%w:\n  - name: %s\n    %s: %s\n    access: {server: {oauth: {}}}", missing, name, source, data)
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
	pkg, port := mcpregistry.Package{}, 0
	if d != nil && (len(d.Packages) != 0 || app.Registry != nil) {
		selection := protocol.PackageSelection{RegistryType: "oci"}
		if app.Registry != nil {
			selection = ptr.Deref(app.Registry.Package, protocol.PackageSelection{})
		}
		var err error
		if pkg, err = mcpregistry.SelectPackage(d, selection); err != nil {
			return err
		}
		if app.Run != nil {
			pkg = mcpregistry.Package{
				RegistryType: pkg.RegistryType, Identifier: pkg.Identifier,
				Transport: mcpregistry.Transport{Type: app.Run.Transport.Type, URL: app.Run.Transport.URL},
			}
		}
		if port, err = pkg.Port(); err != nil {
			return err
		}
	}
	if app.Registry != nil {
		if pkg.RegistryType == "oci" {
			app.resolved.Image = pkg.Identifier
		} else {
			app.resolved.pkg = &protocol.RegistrySource{RegistryRef: app.Registry.RegistryRef, Package: &protocol.PackageSelection{RegistryType: pkg.RegistryType, Identifier: pkg.Identifier}}
		}
	}
	app.resolved.stdio = pkg.Transport.Type == "stdio"
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
	app.resolved.Imported = app.Registry != nil
	if app.resolved.stdio && app.OAuth != nil {
		return errors.New("oauth requires an HTTP MCP server")
	}
	app.resolved.Port = int32(cmp.Or(port, ptr.Deref(app.Port, 0), 3000))
	return nil
}

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
