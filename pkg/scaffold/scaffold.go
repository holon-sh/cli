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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/pelletier/go-toml/v2"

	"github.com/holon-sh/cli/pkg/typegen"
)

//go:embed all:templates
var templates embed.FS

// Versions are the Holon SDK versions an app pins.
type Versions struct {
	HolonSH string // holon-sh on npm, for a TypeScript server
	HolonUI string // holon-ui on npm, for the view
	HolonPy string // holon-sh on PyPI, for a Python server
}

// Languages are the languages an app can be written in, by their --lang names.
var Languages = []string{"go", "py", "ts"}

// Project is an app in Dir, whose MCP server is named Name.
type Project struct {
	Dir, Language, Name string
	Versions            Versions
}

// AppName is the name of the app in dir: the directory's name as a label.
func AppName(dir string) string {
	return typegen.ServerKey(filepath.Base(dir))
}

// manifests are the files a language's existing project keeps its dependencies in.
var manifests = map[string]string{"go": "go.mod", "py": "pyproject.toml", "ts": "package.json"}

// build builds the view: a page into dist/web, and the single file the server returns into dist/mcp.
const build = "vite build && vite build --mode mcp"

// Detect reports whether dir holds a project, and the languages an app there can be written in: any
// into an empty or absent directory, and otherwise the languages whose manifests dir has.
func Detect(dir string) (languages []string, existing bool, err error) {
	if err := Empty(dir); err == nil {
		return Languages, false, nil
	} else if !errors.Is(err, errNotEmpty) {
		return nil, false, err
	}
	for _, language := range Languages {
		if isFile(filepath.Join(dir, manifests[language])) {
			languages = append(languages, language)
		}
	}
	return languages, true, nil
}

var errNotEmpty = errors.New("not empty")

// Empty accepts an empty or absent directory.
func Empty(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case len(entries) > 0:
		return fmt.Errorf("%s is %w", dir, errNotEmpty)
	}
	return nil
}

// Check reports why the app cannot be written: in an existing project, a file the app adds that is
// already there, another server entry, or a project Holon would not run the server of.
func (p Project) Check() error {
	if _, ok := manifests[p.Language]; !ok {
		return fmt.Errorf("unsupported language %q", p.Language)
	}
	_, existing, err := Detect(p.Dir)
	if err != nil || !existing {
		return err
	}
	base := filepath.Base(p.Dir)
	manifest := manifests[p.Language]
	if !isFile(filepath.Join(p.Dir, manifest)) {
		return fmt.Errorf("%s has no %s: holon init adds a %s app beside one, or creates the app in an empty directory", base, manifest, p.Language)
	}
	for _, lock := range []string{"poetry.lock", "pdm.lock"} {
		if p.Language == "py" && isFile(filepath.Join(p.Dir, lock)) {
			return fmt.Errorf("%s has %s: holon init adds Python dependencies with uv, so create the app in its own directory instead, such as holon init %s", base, lock, p.beside())
		}
	}
	if p.Language == "ts" {
		var pkg struct {
			Main    string            `json:"main"`
			Scripts map[string]string `json:"scripts"`
		}
		if err := readJSON(filepath.Join(p.Dir, "package.json"), &pkg); err != nil {
			return err
		}
		if pkg.Main != "" || pkg.Scripts["start"] != "" {
			return fmt.Errorf("%s's package.json has main or a start script, which Holon would run instead of server.ts; create the app in its own directory instead, such as holon init %s", base, p.beside())
		}
	}
	files, err := p.render()
	if err != nil {
		return err
	}
	var conflicts []string
	if entry, err := typegen.SourceEntry(p.Dir); err != nil || entry != "" {
		conflicts = append(conflicts, cmp.Or(entry, "server entries"))
	}
	if p.Language == "go" {
		if sources, _ := filepath.Glob(filepath.Join(p.Dir, "*.go")); len(sources) > 0 && !slices.Contains(conflicts, filepath.Base(sources[0])) {
			conflicts = append(conflicts, filepath.Base(sources[0]))
		}
	}
	for _, name := range []string{"vite.config.ts", "vite.config.js", "vite.config.mjs", "vite.config.mts", "vite.config.cjs", "vite.config.cts"} {
		if exists(filepath.Join(p.Dir, name)) && !slices.Contains(conflicts, name) {
			conflicts = append(conflicts, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		top, _, _ := strings.Cut(name, "/")
		if policies[name] == "" && exists(filepath.Join(p.Dir, top)) && !slices.Contains(conflicts, top) {
			conflicts = append(conflicts, top)
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("%s already has %s; create the app in its own directory instead, such as holon init %s", base, strings.Join(conflicts, ", "), p.beside())
	}
	return nil
}

// beside is a directory of its own to suggest for the app, inside the project, as the working
// directory sees it.
func (p Project) beside() string {
	suggestion := filepath.Join(p.Dir, "mcp")
	if working, err := os.Getwd(); err == nil {
		if relative, err := filepath.Rel(working, suggestion); err == nil {
			return relative
		}
	}
	return suggestion
}

// policies say how Write treats a file the project may already have: merge into it, keep it, or
// write it only into a new project. Other files must be absent.
var policies = map[string]string{
	"package.json":    "merge",
	".gitignore":      "merge",
	"tsconfig.json":   "keep",
	"pyproject.toml":  "keep",
	"go.mod":          "keep",
	".python-version": "new",
}

// Write writes the app after Check accepts it, and returns the files it created and those it
// changed. An existing project keeps its pyproject.toml, tsconfig.json and scripts, gains the npm
// packages the app needs in its package.json, and gains the app's lines in its .gitignore; notes say
// what the project may still need.
func (p Project) Write() (created, changed, notes []string, err error) {
	if err := p.Check(); err != nil {
		return nil, nil, nil, err
	}
	_, existing, err := Detect(p.Dir)
	if err != nil {
		return nil, nil, nil, err
	}
	files, err := p.render()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		data, target := files[name], filepath.Join(p.Dir, filepath.FromSlash(name))
		present := isFile(target)
		switch policies[name] {
		case "keep":
			if present {
				if name == "tsconfig.json" {
					notes = append(notes, "kept tsconfig.json: holon types check uses it, so it must include ui/ with the DOM library and the node and vite/client types")
				}
				continue
			}
		case "new":
			if existing {
				continue
			}
		case "merge":
			var current []byte
			if present {
				if current, err = os.ReadFile(target); err != nil {
					return created, changed, notes, err
				}
				if name == ".gitignore" {
					data = appendLines(current, data)
				} else {
					data = current
				}
			}
			if name == "package.json" {
				var note string
				if data, note, err = p.addPackages(data); err != nil {
					return created, changed, notes, fmt.Errorf("%s: %w", target, err)
				}
				if note != "" {
					notes = append(notes, note)
				}
			}
			if present {
				if bytes.Equal(current, data) {
					continue
				}
				if err := os.WriteFile(target, data, 0o644); err != nil {
					return created, changed, notes, err
				}
				changed = append(changed, name)
				continue
			}
		}
		if err := create(target, data); err != nil {
			return created, changed, notes, err
		}
		created = append(created, name)
	}
	return created, changed, notes, nil
}

// render executes the language's templates and then the view's that the language has no file for,
// by their paths in the project. Go files are kept as .tmpl, so that a go.mod leaves no directory
// out of the embed and no .go file builds with the CLI.
func (p Project) render() (map[string][]byte, error) {
	key, err := json.Marshal(typegen.ServerKey(p.Name))
	if err != nil {
		return nil, err
	}
	dependencies, dev := p.pythonRequirements()
	data := struct {
		App, Name, KeyJSON string
		Python             struct{ Dependencies, Dev string }
	}{App: AppName(p.Dir), Name: p.Name, KeyJSON: string(key)}
	data.Python.Dependencies, data.Python.Dev = tomlArray(dependencies), tomlArray(dev)
	files := map[string][]byte{}
	for _, root := range []string{"templates/" + p.Language, "templates/ui"} {
		err := fs.WalkDir(templates, root, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			relative := strings.TrimSuffix(strings.TrimPrefix(name, root+"/"), ".tmpl")
			if base := path.Base(relative); strings.HasPrefix(base, "_") {
				relative = path.Join(path.Dir(relative), "."+base[1:])
			}
			if _, ok := files[relative]; ok {
				return nil
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
			files[relative] = rendered.Bytes()
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// addPackages adds the npm packages the app needs to package.json data, keeping any version the
// project already declares, and the view's build script when the project has none.
func (p Project) addPackages(data []byte) ([]byte, string, error) {
	var pkg struct {
		Scripts                                                               map[string]string
		Dependencies, DevDependencies, PeerDependencies, OptionalDependencies map[string]string
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, "", err
	}
	dependencies := map[string]string{"@modelcontextprotocol/ext-apps": "^2.0.4", "holon-ui": p.Versions.HolonUI}
	if p.Language == "ts" {
		dependencies["@modelcontextprotocol/server"] = "^2.4.0"
		dependencies["holon-sh"] = p.Versions.HolonSH
		dependencies["zod"] = "^4.6.7"
	}
	dev := map[string]string{"@types/node": "^26.6.6", "typescript": "^7.0.2", "vite": "^8.3.7", "vite-plugin-singlefile": "^2.3.4"}
	for _, section := range []struct {
		name     string
		packages map[string]string
	}{{"dependencies", dependencies}, {"devDependencies", dev}} {
		for _, name := range slices.Sorted(maps.Keys(section.packages)) {
			if pkg.Dependencies[name] != "" || pkg.DevDependencies[name] != "" || pkg.PeerDependencies[name] != "" || pkg.OptionalDependencies[name] != "" {
				continue
			}
			updated, err := typegen.AddPackageMember(data, section.name, name, section.packages[name])
			if err != nil {
				return nil, "", err
			}
			if updated != nil {
				data = updated
			}
		}
	}
	switch current := pkg.Scripts["build"]; current {
	case "":
		updated, err := typegen.AddPackageMember(data, "scripts", "build", build)
		return updated, "", err
	case build:
		return data, "", nil
	default:
		return data, "kept package.json's build script: Holon builds the view with it, so it must also run " + build, nil
	}
}

// requirement captures a Python requirement's project name.
var requirement = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)`)

// PythonRequirements are the requirements the app needs that its pyproject.toml does not declare:
// dependencies, and development ones in any dependency group.
func (p Project) PythonRequirements() (dependencies, dev []string, err error) {
	data, err := os.ReadFile(filepath.Join(p.Dir, "pyproject.toml"))
	if err != nil {
		return nil, nil, err
	}
	var project struct {
		Project struct {
			Dependencies []string `toml:"dependencies"`
		} `toml:"project"`
		Groups map[string][]any `toml:"dependency-groups"`
		Tool   struct {
			UV struct {
				Dev []string `toml:"dev-dependencies"`
			} `toml:"uv"`
		} `toml:"tool"`
	}
	if err := toml.Unmarshal(data, &project); err != nil {
		return nil, nil, fmt.Errorf("pyproject.toml: %w", err)
	}
	declared := map[string]bool{}
	note := func(entries []string) {
		for _, entry := range entries {
			if match := requirement.FindStringSubmatch(entry); match != nil {
				declared[pythonName(match[1])] = true
			}
		}
	}
	note(project.Project.Dependencies)
	note(project.Tool.UV.Dev)
	for _, group := range project.Groups {
		for _, entry := range group {
			if text, ok := entry.(string); ok {
				note([]string{text})
			}
		}
	}
	missing := func(requirements ...string) []string {
		return slices.DeleteFunc(requirements, func(r string) bool {
			return declared[pythonName(requirement.FindStringSubmatch(r)[1])]
		})
	}
	dependencies, dev = p.pythonRequirements()
	return missing(dependencies...), missing(dev...), nil
}

func (p Project) pythonRequirements() (dependencies, dev []string) {
	return []string{"holon-sh==" + p.Versions.HolonPy, "mcp>=2.3,<3"}, []string{"mypy>=2.4,<3"}
}

func tomlArray(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

var separators = regexp.MustCompile(`[-_.]+`)

// pythonName normalizes a Python project name, as PEP 503 compares them.
func pythonName(name string) string {
	return strings.ToLower(separators.ReplaceAllString(name, "-"))
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

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
