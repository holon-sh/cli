package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/holon-sh/cli/pkg/typegen"
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
}

// projectConsumers reads the project manifest in dir and returns a consumer for each path app, as
// holon dev --check selects them. A dependency named after another app in the manifest is that
// app: a path app's source or a remote app's endpoint. Other dependencies keep their sh.holon.requires
// registry reference. Without a manifest it returns nil.
func projectConsumers(dir string) ([]typegen.Consumer, error) {
	var file string
	for _, name := range projectFiles {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			file = filepath.Join(dir, name)
			break
		}
	}
	if file == "" {
		return nil, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var project struct {
		Apps []projectApp `json:"apps"`
	}
	if err := yaml.Unmarshal(data, &project); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	// The apps a dependency can name: each path app's resolved directory, or a remote app's endpoint.
	apps := map[string]typegen.Source{}
	for _, app := range project.Apps {
		switch {
		case filepath.IsAbs(app.Path):
			return nil, fmt.Errorf("%s: %s path must be a relative directory", file, app.Name)
		case app.Path != "":
			path, err := clientDir(filepath.Join(dir, app.Path))
			if err != nil {
				return nil, fmt.Errorf("%s: %s: %w", file, app.Name, err)
			}
			apps[app.Name] = typegen.Source{Path: path}
		case app.Remote != nil:
			apps[app.Name] = typegen.Source{Endpoint: app.Remote.URL}
		}
	}
	var consumers []typegen.Consumer
	for _, app := range project.Apps {
		if app.Path == "" {
			continue
		}
		path := apps[app.Name].Path
		c, err := typegen.AppConsumer(app.Name, path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", app.Name, err)
		}
		if c == nil {
			continue
		}
		requires, err := typegen.DeclaredSources(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", app.Name, err)
		}
		for _, source := range requires {
			if sibling, declared := apps[source.Alias]; declared {
				sibling.Alias = source.Alias
				source = sibling
			}
			c.Sources = append(c.Sources, source)
		}
		consumers = append(consumers, *c)
	}
	if len(consumers) == 0 {
		return nil, fmt.Errorf("%s: no path app declares a Holon SDK runtime or keeps an MCP server", file)
	}
	return consumers, nil
}
