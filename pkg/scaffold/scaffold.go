// Package scaffold writes new Holon apps: an MCP server and the view it serves, in Go, Python or
// TypeScript.
package scaffold

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/holon-sh/cli/pkg/typegen"
)

//go:embed all:templates
var templates embed.FS

// Languages are the languages Write supports, by their --lang names.
var Languages = []string{"go", "py", "ts"}

// Versions are the Holon SDK versions a new app pins.
type Versions struct {
	HolonSH string // holon-sh on npm, for a TypeScript server
	HolonUI string // holon-ui on npm, for the view
	HolonPy string // holon-sh on PyPI, for a Python server
}

// Project is a new app: Name is its MCP server name, and the directory's base name is its app name.
type Project struct {
	Dir      string
	Language string
	Name     string
	Versions Versions
}

// Empty accepts a directory a project can be written into: an empty or absent one.
func Empty(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case len(entries) > 0:
		return fmt.Errorf("%s is not empty", dir)
	}
	return nil
}

// Write writes the project's files into its directory, which must be empty or absent, and returns
// their paths relative to it.
func (p Project) Write() ([]string, error) {
	if !slices.Contains(Languages, p.Language) {
		return nil, fmt.Errorf("unsupported language %q", p.Language)
	}
	if err := Empty(p.Dir); err != nil {
		return nil, err
	}
	key, err := json.Marshal(typegen.ServerKey(p.Name))
	if err != nil {
		return nil, err
	}
	data := struct {
		App, Name, KeyJSON string
		Versions           Versions
	}{filepath.Base(p.Dir), p.Name, string(key), p.Versions}
	var written []string
	for _, root := range []string{"templates/" + p.Language, "templates/ui"} {
		err := fs.WalkDir(templates, root, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			source, err := templates.ReadFile(name)
			if err != nil {
				return err
			}
			parsed, err := template.New(name).Option("missingkey=error").Parse(string(source))
			if err != nil {
				return err
			}
			var rendered bytes.Buffer
			if err := parsed.Execute(&rendered, data); err != nil {
				return err
			}
			// Go files are kept as .tmpl: a go.mod would leave its directory out of the embed, and a .go
			// file would build with the CLI.
			relative := strings.TrimSuffix(strings.TrimPrefix(name, root+"/"), ".tmpl")
			if base := path.Base(relative); strings.HasPrefix(base, "_") {
				relative = path.Join(path.Dir(relative), "."+base[1:])
			}
			// A language's own file replaces the shared one.
			if slices.Contains(written, relative) {
				return nil
			}
			target := filepath.Join(p.Dir, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			if _, err := file.Write(rendered.Bytes()); err != nil {
				file.Close()
				return err
			}
			written = append(written, relative)
			return file.Close()
		})
		if err != nil {
			return written, err
		}
	}
	return written, nil
}
