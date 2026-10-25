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
// named by the requiring alias; every other requires entry becomes a registry app. It returns the
// manifest and the listed apps' names, in argument order; pass them in Options.Apps.
func Directories(dirs []string) (Manifest, []string, error) {
	if len(dirs) == 0 {
		return Manifest{}, nil, errors.New("no directories given")
	}
	type listed struct {
		dir        string
		descriptor *Descriptor
		name       string
	}
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
			return Manifest{}, nil, fmt.Errorf("%s and %s both declare server %s", apps[j].dir, dir, descriptor.Name)
		}
		byServer[descriptor.Name] = i
		apps[i] = listed{dir: dir, descriptor: descriptor}
	}
	// A listed provider takes the alias its callers require it under.
	requiredBy := map[int]int{}
	for i, app := range apps {
		for _, alias := range slices.Sorted(maps.Keys(app.descriptor.Requires)) {
			j, ok := byServer[app.descriptor.Requires[alias].Name]
			if !ok || j == i {
				continue
			}
			if apps[j].name != "" && apps[j].name != alias {
				return Manifest{}, nil, fmt.Errorf("%s is required as both %s (by %s) and %s (by %s); use one alias", apps[j].dir, apps[j].name, apps[requiredBy[j]].dir, alias, app.dir)
			}
			apps[j].name, requiredBy[j] = alias, i
		}
	}
	server := json.RawMessage(`{"server":{}}`)
	m := Manifest{pathRequiresOnly: true}
	names := make([]string, len(apps))
	owner := map[string]string{}
	for i := range apps {
		if apps[i].name == "" {
			apps[i].name = serverKey(apps[i].descriptor.Name)
		}
		name := apps[i].name
		if len(name) > 49 || !appName.MatchString(name) {
			return Manifest{}, nil, fmt.Errorf("%s: server name %s does not give a valid app name (%q); rename the server", apps[i].dir, apps[i].descriptor.Name, name)
		}
		if other, ok := owner[name]; ok {
			return Manifest{}, nil, fmt.Errorf("%s and %s both have the app name %s", other, apps[i].dir, name)
		}
		owner[name] = apps[i].dir
		names[i] = name
		m.Apps = append(m.Apps, App{Name: name, Path: apps[i].dir, Access: server})
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
