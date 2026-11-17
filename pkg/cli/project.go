package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/holon-sh/protocol"
)

// projectFiles are the project manifests holon dev reads by default, in order. Only the fields that
// select apps are read here; holon dev validates the rest.
var projectFiles = []string{"holon.yaml", "holon.yml"}

type projectApp struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Remote *struct {
		URL string `json:"url"`
	} `json:"remote"`
	Registry *protocol.RegistryRef `json:"registry"`
}

// project is a holon.yaml's apps and what a dependency can name: each path app's resolved directory,
// a remote app's endpoint or a registry app's version.
type project struct {
	apps    []projectApp
	paths   map[string]typegen.Path
	remotes map[string]mcpregistry.Requirement
}

// readProject reads the project manifest file; paths are resolved against its directory.
func readProject(file string) (*project, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Apps []projectApp `json:"apps"`
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	p := &project{apps: manifest.Apps, paths: map[string]typegen.Path{}, remotes: map[string]mcpregistry.Requirement{}}
	for _, app := range p.apps {
		switch {
		case filepath.IsAbs(app.Path):
			return nil, fmt.Errorf("%s: %s path must be a relative directory", file, app.Name)
		case app.Path != "":
			path, err := clientDir(filepath.Join(filepath.Dir(file), app.Path))
			if err != nil {
				return nil, fmt.Errorf("%s: %s: %w", file, app.Name, err)
			}
			p.paths[app.Name] = typegen.Path{Dir: path}
		case app.Remote != nil:
			p.remotes[app.Name] = mcpregistry.Requirement{URL: app.Remote.URL}
		case app.Registry != nil:
			p.remotes[app.Name] = mcpregistry.Requirement{RegistryRef: *app.Registry}
		}
	}
	return p, nil
}

// consumer is the appConsumer of the path app, or nil when the app declares no runtime or server.
func (p *project) consumer(app projectApp, resolve bool) (*typegen.Consumer, error) {
	c, err := appConsumer(app.Name, p.paths[app.Name].Dir, p.paths, p.remotes, resolve)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", app.Name, err)
	case c != nil && app.Name == "" && resolve:
		return nil, errors.New("consumer name is required")
	}
	return c, nil
}

// projectConsumers reads the project manifest in dir and returns the appConsumer of each path app.
// A dependency is the manifest's app of that name: a path app's source, a remote app's endpoint or a
// registry app's version. Other dependencies keep what sh.holon.requires says. Without a manifest it
// returns nil.
func projectConsumers(dir string, resolve bool) ([]typegen.Consumer, error) {
	file := projectFile(dir)
	if file == "" {
		return nil, nil
	}
	p, err := readProject(file)
	if err != nil {
		return nil, err
	}
	var consumers []typegen.Consumer
	for _, app := range p.apps {
		if app.Path == "" {
			continue
		}
		switch c, err := p.consumer(app, resolve); {
		case err != nil:
			return nil, err
		case c != nil:
			consumers = append(consumers, *c)
		}
	}
	if len(consumers) == 0 {
		return nil, fmt.Errorf("%s: no path app declares a Holon SDK runtime or keeps an MCP server", file)
	}
	return consumers, nil
}

// enclosingConsumer is the consumer of the path app kept in dir by the nearest holon.yaml above it, so
// holon types generate and check work from inside an app as they do from the project's directory. Only
// that app is read: another app's dependencies cannot fail it. It returns nil when no manifest above dir
// keeps an app there, or that app declares no runtime or server.
func enclosingConsumer(dir string, resolve bool) (*typegen.Consumer, error) {
	for parent := filepath.Dir(dir); ; parent = filepath.Dir(parent) {
		if file := projectFile(parent); file != "" {
			p, err := readProject(file)
			if err != nil {
				return nil, err
			}
			for _, app := range p.apps {
				if app.Path != "" && p.paths[app.Name].Dir == dir {
					return p.consumer(app, resolve)
				}
			}
			return nil, nil
		}
		if filepath.Dir(parent) == parent {
			return nil, nil
		}
	}
}

func projectFile(dir string) string {
	for _, name := range projectFiles {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

var listItem = regexp.MustCompile(`^(\s*)- `)

// addApp adds app, kept at path relative to the manifest's directory, to the holon.yaml at file,
// which it creates when there is none. It appends to an existing file's apps list in place, keeping
// the rest of the file as written, and fails when that list does not end the file.
func addApp(file, app, path string) ([]byte, error) {
	entry := fmt.Sprintf("- name: %s\n  path: %q\n  access:\n    server:\n      oauth: {}\n", app, path)
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte("apps:\n" + indent(entry, "  ")), nil
	}
	if err != nil {
		return nil, err
	}
	before, err := appNames(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if slices.Contains(before, app) {
		return nil, fmt.Errorf("%s already has an app named %s", file, app)
	}
	failed := fmt.Errorf("could not add %s to the apps list in %s; add it there:\n%s", app, file, indent(entry, "  "))
	text := strings.TrimRight(string(data), "\n")
	lines := strings.Split(text, "\n")
	start := slices.IndexFunc(lines, func(line string) bool { return strings.TrimRight(line, " ") == "apps:" })
	if start < 0 {
		return nil, failed
	}
	prefix := "  "
	for _, line := range lines[start+1:] {
		if match := listItem.FindStringSubmatch(line); match != nil {
			prefix = match[1]
			break
		}
	}
	updated := []byte(text + "\n" + indent(entry, prefix))
	if after, err := appNames(updated); err != nil || !slices.Equal(after, append(before, app)) {
		return nil, failed
	}
	return updated, nil
}

func appNames(data []byte) ([]string, error) {
	var project struct{ Apps []projectApp }
	if err := yaml.Unmarshal(data, &project); err != nil {
		return nil, err
	}
	var names []string
	for _, app := range project.Apps {
		names = append(names, app.Name)
	}
	return names, nil
}

func indent(text, prefix string) string {
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "")
}
