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

// Directories builds a standalone manifest from server.json files and returns its local app
// names for Options.Apps. Preparation infers dependencies and filters provider-only directories.
func Directories(dirs []string) (Manifest, []string, error) {
	if len(dirs) == 0 {
		return Manifest{}, nil, errors.New("no directories given")
	}
	server := json.RawMessage(`{"server":{}}`)
	apps := make([]App, len(dirs))
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
		apps[i] = App{Path: dir, Access: server, descriptor: descriptor}
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
		names = append(names, name)
		m.Apps = append(m.Apps, apps[i])
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

func inferDirectoryClients(m Manifest, o *Options, usage map[string]clientUsage) (Manifest, error) {
	m.Apps = slices.Clone(m.Apps)
	providers := map[string]bool{}
	for i, app := range m.Apps {
		if app.Path == "" {
			continue
		}
		d := *app.descriptor
		d.Requires = map[string]Reference{}
		maps.Copy(d.Requires, app.descriptor.Requires)
		required := map[string]bool{d.Name: true}
		for _, ref := range d.Requires {
			required[ref.Name] = true
		}
		for _, other := range m.Apps {
			if other.Path == "" || required[other.descriptor.Name] {
				continue
			}
			key := serverKey(other.descriptor.Name)
			if d.Requires[key].Name != "" || !slices.Contains(usage[app.Name].References, key) {
				continue
			}
			if other.Name != key {
				return m, fmt.Errorf("%s is required as both %s and %s (by %s); use one alias", other.Path, other.Name, key, app.Path)
			}
			d.Requires[key] = Reference{Name: other.descriptor.Name, Version: other.descriptor.Version}
		}
		m.Apps[i].descriptor = &d
		providers[app.Name] = !usage[app.Name].UsesApps && len(d.Requires) == 0
	}
	o.Apps = slices.DeleteFunc(slices.Clone(o.Apps), func(name string) bool { return providers[name] })
	return m, nil
}

// serverKey normalizes the final server-name segment to a client name of at most 49 characters.
func serverKey(serverName string) string {
	name := serverName[strings.LastIndex(serverName, "/")+1:]
	name = strings.Trim(notAppNameRun.ReplaceAllString(strings.ToLower(name), "-"), "-")
	return strings.TrimRight(name[:min(len(name), 49)], "-")
}
