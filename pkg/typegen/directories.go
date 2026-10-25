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

// Directories describes MCP server directories, each containing a server.json, as a Manifest for
// the standalone CLI. Every directory becomes a path app. A requires entry whose server name
// matches another listed directory's server.json is provided by that directory, which is then
// named by the requiring alias; every other requires entry becomes a registry app. Requires need
// not be declared between listed directories: an apps.<name> reference in a directory's source
// that names another listed directory (by the last segment of its server name) is a dependency
// on it. It returns the manifest and the names of the listed apps that use generated clients, in
// argument order; pass them in Options.Apps. A listed directory that references no client and
// imports none is a provider only and gets nothing generated. The returned names are non-nil,
// including when no app needs clients; pass that empty selection through to Options.Apps.
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
	// A client reference to another listed directory is a dependency on it, unless declared.
	for i := range apps {
		references, usesClients, err := clientReferences(apps[i].Path)
		if err != nil {
			return Manifest{}, nil, err
		}
		d := apps[i].descriptor
		apps[i].generates = usesClients || len(d.Requires) > 0
		// Declared aliases and providers win, including a provider required under another alias.
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
	// A listed provider takes the alias its callers require it under.
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

// serverKey derives an app's client name from its registry server name: the last "/" segment,
// lowercased, each run of characters outside [a-z0-9-] replaced with "-", trimmed of "-" and
// truncated to 49 characters (example.com/checkout → checkout, io.github.x/My.Server → my-server).
func serverKey(serverName string) string {
	name := serverName[strings.LastIndex(serverName, "/")+1:]
	name = strings.Trim(notAppNameRun.ReplaceAllString(strings.ToLower(name), "-"), "-")
	return strings.TrimRight(name[:min(len(name), 49)], "-")
}
