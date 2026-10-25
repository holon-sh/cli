package typegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var notAppNameRun = regexp.MustCompile(`[^a-z0-9-]+`)

// Directories builds a standalone manifest from server.json files. Listed servers satisfy
// declared dependencies and source references; other dependencies come from their registries.
// Declared aliases and versions take precedence. Pass the returned names to Options.Apps:
// they select directories that import or reference clients or declare requires, in argument
// order. An empty, non-nil selection leaves every provider untouched.
func Directories(dirs []string) (Manifest, []string, error) {
	if len(dirs) == 0 {
		return Manifest{}, nil, errors.New("no directories given")
	}
	type listed struct {
		App
		generates bool
	}
	server := json.RawMessage(`{"server":{}}`)
	apps := make([]listed, len(dirs))
	byServer := map[string]int{}
	for i, dir := range dirs {
		dir, err := filepath.Abs(dir)
		if err != nil {
			return Manifest{}, nil, err
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return Manifest{}, nil, fmt.Errorf("%s is not a directory", dir)
		}
		path := filepath.Join(dir, "server.json")
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, nil, fmt.Errorf("%s: no server.json found; run this inside an MCP server directory with a server.json, or pass DIR...", dir)
		}
		if err != nil {
			return Manifest{}, nil, err
		}
		descriptor, err := ParseDescriptor(data)
		if err != nil {
			return Manifest{}, nil, fmt.Errorf("%s: %w", path, err)
		}
		if j, ok := byServer[descriptor.Name]; ok {
			return Manifest{}, nil, fmt.Errorf("%s and %s both declare server %s", apps[j].Path, dir, descriptor.Name)
		}
		byServer[descriptor.Name] = i
		apps[i] = listed{App: App{Path: dir, Access: server, descriptor: descriptor}}
	}
	for i := range apps {
		references, usesClients, err := clientReferences(apps[i].Path)
		if err != nil {
			return Manifest{}, nil, err
		}
		d := apps[i].descriptor
		apps[i].generates = usesClients || len(d.Requires) > 0
		required := map[string]bool{d.Name: true}
		for alias, ref := range d.Requires {
			delete(references, alias)
			required[ref.Name] = true
		}
		for _, other := range apps {
			key := serverKey(other.descriptor.Name)
			if references[key] && !required[other.descriptor.Name] {
				if d.Requires == nil {
					d.Requires = map[string]Reference{}
				}
				d.Requires[key] = Reference{Name: other.descriptor.Name, Version: other.descriptor.Version}
			}
		}
	}
	requiredBy := map[int]int{}
	for i, app := range apps {
		for _, alias := range slices.Sorted(maps.Keys(app.descriptor.Requires)) {
			j, ok := byServer[app.descriptor.Requires[alias].Name]
			if !ok || j == i {
				continue
			}
			if apps[j].Name != "" && apps[j].Name != alias {
				return Manifest{}, nil, fmt.Errorf("%s is required as both %s (by %s) and %s (by %s); use one alias", apps[j].Path, apps[j].Name, apps[requiredBy[j]].Path, alias, app.Path)
			}
			apps[j].Name, requiredBy[j] = alias, i
		}
	}
	m := Manifest{pathRequiresOnly: true}
	names := []string{}
	owner := map[string]string{}
	for i := range apps {
		if apps[i].Name == "" {
			apps[i].Name = serverKey(apps[i].descriptor.Name)
		}
		name := apps[i].Name
		if !appName.MatchString(name) {
			return Manifest{}, nil, fmt.Errorf("%s: server name %s does not give a valid app name (%q); rename the server", apps[i].Path, apps[i].descriptor.Name, name)
		}
		if other, ok := owner[name]; ok {
			return Manifest{}, nil, fmt.Errorf("%s and %s both have the app name %s", other, apps[i].Path, name)
		}
		owner[name] = apps[i].Path
		if apps[i].generates {
			names = append(names, name)
		}
		m.Apps = append(m.Apps, apps[i].App)
	}
	for _, app := range apps {
		for _, alias := range slices.Sorted(maps.Keys(app.descriptor.Requires)) {
			if _, ok := owner[alias]; ok {
				continue
			}
			ref := app.descriptor.Requires[alias]
			ref.Registry = ref.RegistryURL()
			owner[alias] = ref.Registry
			m.Apps = append(m.Apps, App{Name: alias, Registry: &RegistrySource{Reference: ref}, Access: server})
		}
	}
	return m, names, nil
}

// serverKey normalizes the final server-name segment to a client name of at most 49 characters.
func serverKey(serverName string) string {
	name := serverName[strings.LastIndex(serverName, "/")+1:]
	name = strings.Trim(notAppNameRun.ReplaceAllString(strings.ToLower(name), "-"), "-")
	return strings.TrimRight(name[:min(len(name), 49)], "-")
}
