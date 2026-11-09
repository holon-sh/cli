// Package scaffold writes Holon apps: an MCP server and the view it serves, in Go, Python or
// TypeScript, into a new directory or beside an existing project in that language.
package scaffold

import (
	"bytes"
	"cmp"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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

// Versions are the Holon SDK versions an app pins.
type Versions struct {
	HolonSH    string // holon-sh on npm, for a TypeScript server
	HolonUI    string // holon-ui on npm, for the view
	HolonPy    string // holon-sh on PyPI, for a Python server
	HolonTypes string
}

// Languages are the languages an app can be written in, by their --lang names.
var Languages = []string{typegen.TargetGo, typegen.TargetPy, typegen.TargetTS}

// Project is an app in Dir, whose MCP server is named Name.
type Project struct {
	Dir, Language, Name string
	Versions            Versions
}

// AppName is the name of the app in dir: the directory's name as a label.
func AppName(dir string) string {
	return typegen.ServerKey(filepath.Base(dir))
}

// build builds the view: a page into dist/web, and the single file the server returns into dist/mcp.
const build = "vite build && vite build --mode mcp"

// shared are the files an existing project may already have: the app merges into package.json and
// .gitignore, and leaves the others as written.
var shared = []string{"package.json", ".gitignore", "tsconfig.json", "pyproject.toml", "go.mod", ".python-version"}

// Detect reports whether dir holds a project, and the languages an app there can be written in: any
// into an empty or absent directory, and otherwise the languages whose manifests dir has.
func Detect(dir string) (languages []string, existing bool, err error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) || err == nil && len(entries) == 0 {
		return Languages, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	for _, language := range Languages {
		if isFile(filepath.Join(dir, typegen.Manifests[language])) {
			languages = append(languages, language)
		}
	}
	return languages, true, nil
}

// Check reports why the app cannot be written: in an existing project, a file the app adds that is
// already there, another server entry, or a project Holon would not run the server of.
func (p Project) Check() error {
	_, existing, err := Detect(p.Dir)
	if err != nil || !existing {
		return err
	}
	base := filepath.Base(p.Dir)
	manifest := filepath.Join(p.Dir, typegen.Manifests[p.Language])
	if !isFile(manifest) {
		return fmt.Errorf("%s has no %s: holon init adds a %s app beside one, or creates the app in an empty directory", base, filepath.Base(manifest), p.Language)
	}
	switch p.Language {
	case typegen.TargetPy:
		for _, lock := range []string{"poetry.lock", "pdm.lock"} {
			if isFile(filepath.Join(p.Dir, lock)) {
				return fmt.Errorf("%s has %s: holon init adds Python dependencies with uv, so create the app in its own directory instead, such as holon init %s", base, lock, p.beside())
			}
		}
	case typegen.TargetTS:
		var pkg typegen.PackageJSON
		data, err := os.ReadFile(manifest)
		if err == nil {
			err = json.Unmarshal(data, &pkg)
		}
		if err != nil {
			return err
		}
		if pkg.Launches() {
			return fmt.Errorf("%s's package.json has main or a start script, which Holon would run instead of server.ts; create the app in its own directory instead, such as holon init %s", base, p.beside())
		}
	}
	files, err := p.templates()
	if err != nil {
		return err
	}
	var conflicts []string
	if entry, err := typegen.SourceEntry(p.Dir); err != nil || entry != "" {
		conflicts = append(conflicts, cmp.Or(entry, "server entries"))
	}
	patterns := []string{"vite.config.*"}
	if p.Language == typegen.TargetGo {
		patterns = append(patterns, "*.go")
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(p.Dir, pattern))
		for _, match := range matches {
			conflicts = append(conflicts, filepath.Base(match))
		}
	}
	for name := range files {
		top, _, _ := strings.Cut(name, "/")
		if !slices.Contains(shared, name) && exists(filepath.Join(p.Dir, top)) {
			conflicts = append(conflicts, top)
		}
	}
	slices.Sort(conflicts)
	if conflicts = slices.Compact(conflicts); len(conflicts) > 0 {
		return fmt.Errorf("%s already has %s; create the app in its own directory instead, such as holon init %s", base, strings.Join(conflicts, ", "), p.beside())
	}
	return nil
}

// beside is a directory of its own to suggest for the app, inside the project, as the working
// directory sees it.
func (p Project) beside() string {
	return Relative(filepath.Join(p.Dir, "mcp"))
}

// Relative is path relative to the working directory when that is shorter to read.
func Relative(path string) string {
	if working, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(working, path); err == nil && filepath.IsLocal(rel) {
			return rel
		}
	}
	return path
}

// Write writes the app after Check accepts it, and returns the files it created and those it
// changed. An existing project keeps its manifest, tsconfig.json and scripts, gains the npm packages
// the app needs in its package.json and the app's lines in its .gitignore; notes say what the
// project may still need.
func (p Project) Write() (created, changed, notes []string, err error) {
	if err := p.Check(); err != nil {
		return nil, nil, nil, err
	}
	existing := isFile(filepath.Join(p.Dir, typegen.Manifests[p.Language]))
	files, err := p.render()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		data, target := files[name], filepath.Join(p.Dir, filepath.FromSlash(name))
		current, err := os.ReadFile(target)
		present := err == nil
		switch {
		case name == ".python-version" && existing:
			continue
		case present && slices.Contains([]string{"tsconfig.json", "pyproject.toml", "go.mod"}, name):
			if name == "tsconfig.json" {
				notes = append(notes, "kept tsconfig.json: holon types check uses it, so it must include ui/ with the DOM library and the node and vite/client types")
			}
			continue
		case name == ".gitignore" && present:
			data = appendLines(current, data)
		case name == "package.json":
			if present {
				data = current
			}
			var note string
			if data, note, err = p.addPackages(data); err != nil {
				return created, changed, notes, fmt.Errorf("%s: %w", target, err)
			}
			if note != "" {
				notes = append(notes, note)
			}
		}
		switch {
		case !present:
			if err := create(target, data); err != nil {
				return created, changed, notes, err
			}
			created = append(created, name)
		case !bytes.Equal(current, data):
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return created, changed, notes, err
			}
			changed = append(changed, name)
		}
	}
	return created, changed, notes, nil
}

// templates maps each file of the app to the template it is rendered from: the language's own, and
// the view's that the language has none for. Go files are kept as .tmpl, so that a go.mod leaves no
// directory out of the embed and no .go file builds with the CLI; dotfiles are kept as _name.
func (p Project) templates() (map[string]string, error) {
	files := map[string]string{}
	for _, root := range []string{"templates/" + p.Language, "templates/ui"} {
		err := fs.WalkDir(templates, root, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			relative := strings.TrimSuffix(strings.TrimPrefix(name, root+"/"), ".tmpl")
			if base := path.Base(relative); strings.HasPrefix(base, "_") {
				relative = path.Join(path.Dir(relative), "."+base[1:])
			}
			if _, ok := files[relative]; !ok {
				files[relative] = name
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// render executes the app's templates, by the paths of their files in the project.
func (p Project) render() (map[string][]byte, error) {
	sources, err := p.templates()
	if err != nil {
		return nil, err
	}
	data := struct{ App, Name, Key string }{AppName(p.Dir), p.Name, typegen.ServerKey(p.Name)}
	files := map[string][]byte{}
	for relative, name := range sources {
		source, err := templates.ReadFile(name)
		if err != nil {
			return nil, err
		}
		parsed, err := template.New(name).Parse(string(source))
		if err != nil {
			return nil, err
		}
		var rendered bytes.Buffer
		if err := parsed.Execute(&rendered, data); err != nil {
			return nil, err
		}
		files[relative] = rendered.Bytes()
	}
	return files, nil
}

// addPackages adds the npm packages the app needs to package.json data, and the view's build script
// when the project has none.
func (p Project) addPackages(data []byte) ([]byte, string, error) {
	var pkg typegen.PackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, "", err
	}
	dependencies := map[string]string{"@modelcontextprotocol/ext-apps": "^2.0.4", typegen.NodePackages[typegen.TargetUI]: p.Versions.HolonUI}
	if p.Language == typegen.TargetTS {
		dependencies["@modelcontextprotocol/server"] = "^2.4.0"
		dependencies[typegen.NodePackages[typegen.TargetTS]] = p.Versions.HolonSH
		dependencies["zod"] = "^4.6.7"
	}
	dev := map[string]string{"@types/node": "^26.6.6", "typescript": "^7.0.2", "vite": "^8.3.7", "vite-plugin-singlefile": "^2.3.4"}
	if p.Language == typegen.TargetTS {
		dev[cliPackage] = p.Versions.HolonTypes
	}
	for _, section := range []struct {
		name     string
		packages map[string]string
	}{{"dependencies", dependencies}, {"devDependencies", dev}} {
		for _, name := range slices.Sorted(maps.Keys(section.packages)) {
			object := section.name
			if declared := pkg.Section(name); declared != "" {
				if !holonPackages[name] {
					continue
				}
				object = declared
			}
			updated, err := typegen.SetPackageMember(data, object, name, section.packages[name])
			if err != nil {
				return nil, "", err
			}
			if updated != nil {
				data = updated
			}
		}
	}
	switch current, ok := pkg.Scripts["build"]; {
	case !ok:
		updated, err := typegen.SetPackageMember(data, "scripts", "build", build)
		return updated, "", err
	case current == build:
		return data, "", nil
	default:
		return data, "kept package.json's build script: Holon builds the view with it, so it must also run " + build, nil
	}
}

// PythonRequirements are the Python requirements the app needs: dependencies, which a project
// declares directly, and development ones, which it only needs installed.
func (p Project) PythonRequirements() (dependencies, dev []string) {
	return []string{typegen.PythonRuntime + "==" + p.Versions.HolonPy, "mcp>=2.3,<3"}, []string{cliPackage + "==" + p.Versions.HolonTypes}
}

// appendLines appends to a file's text each line of more it lacks.
func appendLines(current, more []byte) []byte {
	key := func(line string) string { return strings.TrimSuffix(strings.TrimSpace(line), "/") }
	have := map[string]bool{}
	for line := range strings.Lines(string(current)) {
		have[key(line)] = true
	}
	out := current
	for line := range strings.Lines(string(more)) {
		if !have[key(line)] {
			if len(out) > 0 && out[len(out)-1] != '\n' {
				out = append(out, '\n')
			}
			out = append(out, line...)
		}
	}
	return out
}

func create(target string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
