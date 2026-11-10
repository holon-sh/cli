package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/modfile"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/pkg/mcptype"
)

// PackageJSON is what Holon reads of a package.json.
type PackageJSON struct {
	Name, Version, Main                                                   string
	PackageManager                                                        string `json:"packageManager"`
	Scripts                                                               map[string]string
	Dependencies, DevDependencies, PeerDependencies, OptionalDependencies map[string]string
	Holon                                                                 struct {
		ContractVersion int `json:"contractVersion"`
	}
}

// Launches reports whether the package starts itself, through main or a start script, so that
// Holon runs it instead of importing its server entry.
func (p PackageJSON) Launches() bool {
	return p.Main != "" || p.Scripts["start"] != ""
}

func (p PackageJSON) Declares(pkg string) bool {
	return p.Dependencies[pkg] != "" || p.DevDependencies[pkg] != "" || p.PeerDependencies[pkg] != "" || p.OptionalDependencies[pkg] != ""
}

// packageManager is the project's declared package manager, else the one its nearest lockfile
// belongs to, else npm.
func packageManager(dir string, pkg PackageJSON) string {
	if manager, _, _ := strings.Cut(pkg.PackageManager, "@"); manager != "" {
		return manager
	}
	for current := range ancestors(dir) {
		for _, lock := range []struct{ file, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"package-lock.json", "npm"}} {
			if isFile(filepath.Join(current, lock.file)) {
				return lock.manager
			}
		}
	}
	return "npm"
}

// PackageManager is the package manager of the Node.js project in dir: the one its package.json
// declares, else the one its nearest lockfile belongs to, else npm.
func PackageManager(dir string) (string, error) {
	var manifest PackageJSON
	if err := readTypeJSON(filepath.Join(dir, "package.json"), &manifest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return packageManager(dir, manifest), nil
}

// Install installs missing declared SDK runtimes with the project's package manager, and a Python
// project's dependencies with uv when it has no environment, and maps #holon-sh and #holon-ui to the
// clients in package.json. Call it before snapshotting sources: both can change the project's package
// files.
func Install(ctx context.Context, consumers []Consumer, options Options) error {
	o := options.defaults()
	for _, c := range consumers {
		if err := c.validate(); err != nil {
			return err
		}
		for _, target := range c.Targets {
			var err error
			switch target {
			case TargetTS, TargetUI:
				if err = ensureNodeRuntime(ctx, c.Dir, NodePackages[target], o); err == nil {
					err = mapClients(c.Dir, target, o)
				}
			case TargetPy:
				if err = ensurePythonRuntime(c.Dir); err == nil {
					err = ensurePythonEnvironment(ctx, c.Dir, o)
				}
			}
			if err != nil {
				return fmt.Errorf("%s: %w", c.Name, err)
			}
		}
	}
	return nil
}

func ensureNodeRuntime(ctx context.Context, dir, pkg string, o Options) error {
	const installCommand = "install"
	if installedRuntime(dir, pkg) == nil {
		return nil
	}
	var manifest PackageJSON
	if err := readTypeJSON(filepath.Join(dir, "package.json"), &manifest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s has no package.json; create one and run npm install %s", dir, pkg)
		}
		return err
	}
	manager := packageManager(dir, manifest)
	if !manifest.Declares(pkg) {
		verb := "add"
		if manager == "npm" {
			verb = installCommand
		}
		return fmt.Errorf("%s does not depend on %s; run %s %s %s", dir, pkg, manager, verb, pkg)
	}
	fmt.Fprintf(o.Stderr, "%s: %s %s\n", filepath.Base(dir), manager, installCommand)
	if err := validateEnv(o.Env); err != nil {
		return err
	}
	if err := commandenv.RunStreaming(ctx, dir, manager, []string{installCommand}, nil, append(os.Environ(), o.Env...), o.Stderr, o.Stderr); err != nil {
		return fmt.Errorf("%s %s in %s: %w", manager, installCommand, dir, err)
	}
	return installedRuntime(dir, pkg)
}

// ensurePythonEnvironment installs a Python project's dependencies into .venv with uv, as holon init does,
// unless it has an environment of its own: the counterpart of ensureNodeRuntime's install.
func ensurePythonEnvironment(ctx context.Context, dir string, o Options) error {
	if pythonEnvironment(dir, "") != "" {
		return nil
	}
	commands := [][]string{{"uv", "sync"}}
	if !isFile(filepath.Join(dir, "pyproject.toml")) {
		commands = [][]string{{"uv", "venv"}, {"uv", "pip", "install", "-r", "requirements.txt"}}
	}
	if err := validateEnv(o.Env); err != nil {
		return err
	}
	for _, command := range commands {
		line := strings.Join(command, " ")
		fmt.Fprintf(o.Stderr, "%s: %s\n", filepath.Base(dir), line)
		if err := commandenv.RunStreaming(ctx, dir, command[0], command[1:], nil, append(os.Environ(), o.Env...), o.Stderr, o.Stderr); err != nil {
			return fmt.Errorf("%s in %s: %w", line, dir, err)
		}
	}
	return nil
}

// mapClients gives a Node.js target's clients the import name #holon-sh or #holon-ui in the package.json
// at dir, unless it already maps that name. Without a package.json there, nothing would resolve it.
func mapClients(dir, target string, o Options) error {
	file := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	name := "#" + NodePackages[target]
	if data, err = AddPackageMember(data, "imports", name, "./"+slots[target]+"/"+mcptype.TypeScriptModule); err != nil || data == nil {
		return err
	}
	fmt.Fprintf(o.Stderr, "%s: mapped %s in package.json\n", filepath.Base(dir), name)
	return os.WriteFile(file, data, 0o644)
}

// AddPackageMember returns the package.json data with name set to value as the last member of its
// top-level object member object, which it adds last when absent, laid out as the file lays out its
// first member and leaving the rest as written; nil when object already has name.
func AddPackageMember(data []byte, object, name, value string) ([]byte, error) {
	entry := fmt.Sprintf("%q: %q", name, value)
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, errors.New("package.json is not a JSON object")
	}
	open := int(decoder.InputOffset())
	newline, indent := " ", ""
	space := data[open : len(data)-len(bytes.TrimLeft(data[open:], " \t\r\n"))]
	if i := bytes.LastIndexByte(space, '\n'); i >= 0 {
		newline, indent = "\n", string(space[i+1:])
		if i > 0 && space[i-1] == '\r' {
			newline = "\r\n"
		}
	}
	// member inserts text as the last member of an object that opens at start and has members up to end.
	member := func(start, end int, depth string, text string) []byte {
		insert := "," + newline + depth + indent + text
		if end == start {
			insert = newline + depth + indent + text + newline + depth
		}
		return slices.Concat(data[:end], []byte(insert), data[end:])
	}
	// skip reads past a member's value and reports where it ends.
	skip := func() (int, error) {
		err := decoder.Decode(new(json.RawMessage))
		return int(decoder.InputOffset()), err
	}
	end := open
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if key == object {
			if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
				return nil, fmt.Errorf("package.json %s is not a JSON object", object)
			}
			start := int(decoder.InputOffset())
			last := start
			for decoder.More() {
				if key, err = decoder.Token(); err != nil || key == name {
					return nil, err
				}
				if last, err = skip(); err != nil {
					return nil, err
				}
			}
			return member(start, last, indent, entry), nil
		}
		if end, err = skip(); err != nil {
			return nil, err
		}
	}
	return member(open, end, "", strconv.Quote(object)+": {"+newline+indent+indent+entry+newline+indent+"}"), nil
}

// ErrNoRuntime identifies a project without a declared Holon SDK runtime.
var ErrNoRuntime = errors.New("declares no runtime: depend on holon-sh or holon-ui in package.json, on holon-sh in pyproject.toml, or on " + GoRuntimeModule + " in go.mod")

// GoRuntimeModule is the runtime module that generated Go clients import.
const GoRuntimeModule = "github.com/holon-sh/sdk-go"

// DeclaredTargets are the slots a project's SDK dependencies declare. None is an error.
func DeclaredTargets(dir string) ([]string, error) {
	var targets []string
	var manifest PackageJSON
	if err := readTypeJSON(filepath.Join(dir, "package.json"), &manifest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, target := range []string{TargetTS, TargetUI} {
		if manifest.Declares(NodePackages[target]) {
			targets = append(targets, target)
		}
	}
	if ensurePythonRuntime(dir) == nil {
		targets = append(targets, TargetPy)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && requiresGoRuntime(data) {
		targets = append(targets, TargetGo)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("%s %w", dir, ErrNoRuntime)
	}
	return targets, nil
}

func requiresGoRuntime(data []byte) bool {
	file, err := modfile.ParseLax("go.mod", data, nil)
	return err == nil && slices.ContainsFunc(file.Require, func(require *modfile.Require) bool { return require.Mod.Path == GoRuntimeModule })
}

// PythonRuntime is the Python package that generated Python clients import.
const PythonRuntime = "holon-sh"

// ensurePythonRuntime requires holon-sh among the project's declared Python dependencies.
func ensurePythonRuntime(dir string) error {
	declared, err := pythonDependencies(dir)
	if err != nil {
		return err
	}
	if !declared[PythonRuntime] {
		return fmt.Errorf("%s does not depend on holon-sh; run uv add holon-sh (or add it to requirements.txt)", dir)
	}
	return nil
}

// requirementName captures the project name a Python requirement starts with.
var requirementName = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)`)

var nameSeparators = regexp.MustCompile(`[-_.]+`)

// PythonName is a Python requirement's project name, normalized as PEP 503 compares names.
func PythonName(requirement string) string {
	match := requirementName.FindStringSubmatch(requirement)
	if match == nil {
		return ""
	}
	return strings.ToLower(nameSeparators.ReplaceAllString(match[1], "-"))
}

// pythonDependencies are the projects a Python project in dir declares in any scope, by PythonName:
// pyproject.toml's dependencies, optional dependencies, dependency groups, and uv's, Poetry's and
// PDM's tables, and requirements files.
func pythonDependencies(dir string) (map[string]bool, error) {
	declared := map[string]bool{}
	note := func(requirements ...string) {
		for _, requirement := range requirements {
			if name := PythonName(requirement); name != "" {
				declared[name] = true
			}
		}
	}
	for _, name := range []string{"requirements.txt", "requirements.in"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			note(strings.Split(string(data), "\n")...)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return declared, nil
	}
	if err != nil {
		return nil, err
	}
	type poetryTable struct {
		Dependencies map[string]any `toml:"dependencies"`
	}
	var project struct {
		Project struct {
			Dependencies         []string            `toml:"dependencies"`
			OptionalDependencies map[string][]string `toml:"optional-dependencies"`
		} `toml:"project"`
		Groups map[string][]any `toml:"dependency-groups"`
		Tool   struct {
			UV struct {
				Dev []string `toml:"dev-dependencies"`
			} `toml:"uv"`
			Poetry struct {
				poetryTable
				Dev   map[string]any         `toml:"dev-dependencies"`
				Group map[string]poetryTable `toml:"group"`
			} `toml:"poetry"`
			PDM struct {
				Dev map[string][]string `toml:"dev-dependencies"`
			} `toml:"pdm"`
		} `toml:"tool"`
	}
	if err := toml.Unmarshal(data, &project); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "pyproject.toml"), err)
	}
	note(project.Project.Dependencies...)
	note(project.Tool.UV.Dev...)
	for _, entries := range project.Groups {
		for _, entry := range entries {
			if requirement, ok := entry.(string); ok {
				note(requirement)
			}
		}
	}
	for _, optional := range project.Project.OptionalDependencies {
		note(optional...)
	}
	for _, dev := range project.Tool.PDM.Dev {
		note(dev...)
	}
	note(slices.Collect(maps.Keys(project.Tool.Poetry.Dependencies))...)
	note(slices.Collect(maps.Keys(project.Tool.Poetry.Dev))...)
	for _, group := range project.Tool.Poetry.Group {
		note(slices.Collect(maps.Keys(group.Dependencies))...)
	}
	return declared, nil
}
