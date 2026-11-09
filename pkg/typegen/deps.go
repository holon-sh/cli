package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/holon-sh/cli/pkg/mcptype"
)

type packageJSON struct {
	Name, Version                                                         string
	PackageManager                                                        string `json:"packageManager"`
	Dependencies, DevDependencies, PeerDependencies, OptionalDependencies map[string]string
	Holon                                                                 struct {
		ContractVersion int `json:"contractVersion"`
	}
}

func (p packageJSON) declares(pkg string) bool {
	return p.Dependencies[pkg] != "" || p.DevDependencies[pkg] != "" || p.PeerDependencies[pkg] != "" || p.OptionalDependencies[pkg] != ""
}

// packageManager is the project's declared package manager, else the one its nearest lockfile
// belongs to, else npm.
func packageManager(dir string, pkg packageJSON) string {
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

// Install installs missing declared SDK runtimes with the project's package manager, and maps
// #holon-sh and #holon-ui to the clients in package.json. Call it before snapshotting sources: both
// can change the project's package files.
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
				if err = ensureNodeRuntime(ctx, c.Dir, nodePackages[target], o); err == nil {
					err = mapClients(c.Dir, target, o)
				}
			case TargetPy:
				err = ensurePythonRuntime(c.Dir)
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
	var manifest packageJSON
	if err := readTypeJSON(filepath.Join(dir, "package.json"), &manifest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s has no package.json; create one and run npm install %s", dir, pkg)
		}
		return err
	}
	manager := packageManager(dir, manifest)
	if !manifest.declares(pkg) {
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
	command := exec.CommandContext(ctx, manager, installCommand)
	command.Dir = dir
	command.Env = append(command.Environ(), o.Env...)
	command.Stdout, command.Stderr = o.Stderr, o.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s %s in %s: %w", manager, installCommand, dir, err)
	}
	return installedRuntime(dir, pkg)
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
	name := "#" + nodePackages[target]
	if data, err = addImport(data, name, "./"+slots[target]+"/"+mcptype.TypeScriptModule); err != nil || data == nil {
		return err
	}
	fmt.Fprintf(o.Stderr, "%s: mapped %s in package.json\n", filepath.Base(dir), name)
	return os.WriteFile(file, data, 0o644)
}

// addImport returns the package.json data with name mapped to target as its last import, laid out as
// the file lays out its first member and leaving the rest as written; nil when it already maps name.
func addImport(data []byte, name, target string) ([]byte, error) {
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
	entry := fmt.Sprintf("%q: %q", name, target)
	end := open
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if key == "imports" {
			if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
				return nil, errors.New("package.json imports is not a JSON object")
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
	return member(open, end, "", `"imports": {`+newline+indent+indent+entry+newline+indent+"}"), nil
}

// ErrNoRuntime identifies a project without a declared Holon SDK runtime.
var ErrNoRuntime = errors.New("declares no runtime: depend on holon-sh or holon-ui in package.json, on holon-sh in pyproject.toml, or on " + goRuntimeModule + " in go.mod")

// goRuntimeModule is the runtime module that generated Go clients import.
const goRuntimeModule = "github.com/holon-sh/sdk-go"

// DeclaredTargets are the slots a project's SDK dependencies declare. None is an error.
func DeclaredTargets(dir string) ([]string, error) {
	var targets []string
	var manifest packageJSON
	if err := readTypeJSON(filepath.Join(dir, "package.json"), &manifest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, target := range []string{TargetTS, TargetUI} {
		if manifest.declares(nodePackages[target]) {
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
	return err == nil && slices.ContainsFunc(file.Require, func(require *modfile.Require) bool { return require.Mod.Path == goRuntimeModule })
}

var pythonRuntime = regexp.MustCompile(`(?im)(?:^|["'\s])holon[-_]sh(?:\[|[\s"'<=>!~;]|$)`)

// ensurePythonRuntime requires holon-sh among the project's declared Python dependencies.
func ensurePythonRuntime(dir string) error {
	for _, name := range []string{"pyproject.toml", "requirements.txt", "requirements.in"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil && pythonRuntime.Match(data) {
			return nil
		}
	}
	return fmt.Errorf("%s does not depend on holon-sh; run uv add holon-sh (or add it to requirements.txt)", dir)
}
