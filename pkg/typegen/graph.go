package typegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

func resolveManifestGraph(ctx context.Context, m Manifest, o Options) (Manifest, error) {
	declarations := map[string]App{}
	for _, app := range m.Apps {
		declarations[app.Name] = app
	}
	queue := slices.Clone(o.Apps)
	if len(queue) == 0 {
		for _, app := range m.Apps {
			queue = append(queue, app.Name)
		}
	}
	nodes := map[string]App{}
	selections := map[string]Reference{}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, ok := nodes[name]; ok {
			continue
		}
		app, ok := declarations[name]
		if !ok {
			return m, fmt.Errorf("dependency %s must be declared in apps with explicit access.server", name)
		}
		var err error
		if app.Path != "" {
			data, readErr := os.ReadFile(filepath.Join(app.Path, "server.json"))
			if readErr == nil {
				app.descriptor, err = ParseDescriptor(data)
			} else if !errors.Is(readErr, os.ErrNotExist) {
				err = readErr
			}
		}
		if app.Registry != nil {
			app.descriptor, err = (Client{HTTP: o.HTTPClient, Origin: app.Registry.RegistryURL()}).Get(ctx, app.Registry.Name, app.Registry.Version)
		}
		if err != nil && app.Registry != nil && ctx.Err() == nil && contractUnavailable(err) {
			app.unavailable = err.Error()
			err = nil
		}
		if err != nil {
			return m, fmt.Errorf("%s descriptor: %w", name, err)
		}
		nodes[name] = app
		if app.descriptor != nil {
			for dep, ref := range app.descriptor.Requires {
				ref.Registry = ref.RegistryURL()
				if old, ok := selections[dep]; ok && old != ref {
					return m, fmt.Errorf("conflicting references for dependency %s", dep)
				}
				selections[dep] = ref
				queue = append(queue, dep)
			}
		}
	}
	for name, app := range nodes {
		if app.descriptor == nil {
			continue
		}
		for dep, ref := range app.descriptor.Requires {
			selected := nodes[dep]
			var access map[string]json.RawMessage
			_ = json.Unmarshal(selected.Access, &access)
			if access["server"] == nil || string(access["server"]) == "null" {
				return m, fmt.Errorf("%s dependency %s must be declared in apps with explicit access.server", name, dep)
			}
			if selected.Path != "" && (selected.descriptor == nil || selected.descriptor.Identity() != ref.Identity()) {
				return m, fmt.Errorf("%s requires %s to be %s@%s; declared app does not match", name, dep, ref.Name, ref.Version)
			}
			if selected.Registry != nil {
				if selected.Registry.Identity() != ref.Identity() {
					return m, fmt.Errorf("%s requires %s to be %s@%s; declared app does not match", name, dep, ref.Name, ref.Version)
				}
				if selected.Registry.RegistryURL() != ref.RegistryURL() {
					return m, fmt.Errorf("conflicting registries for dependency %s", dep)
				}
			}
		}
	}
	m.Apps = nil
	for _, a := range nodes {
		m.Apps = append(m.Apps, a)
	}
	slices.SortFunc(m.Apps, func(a, b App) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return m, nil
}
