// Package recipe decides how a development environment installs, builds and starts an app from its source.
package recipe

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/holon-sh/protocol/runtimecontract"
)

// Recipe is how a development environment runs a project: Install whenever a file in Inputs changes, then Build.
type Recipe struct {
	NodeVersionNotice string // Reason a later install can select a different Node version.
	Inputs            []string
	Install           [][]string
	Build             [][]string
	Deploy            Deploy
}

type Deploy struct {
	Install [][]string
	Build   [][]string
	Freeze  runtimecontract.Freeze
}

// Read returns the recipe of the npm, pnpm or uv project in source.
func Read(source fs.FS) (Recipe, error) {
	var files []string
	err := fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, name)
		}
		return err
	})
	if err != nil {
		return Recipe{}, err
	}
	if python := present(files, "pyproject.toml", "requirements.txt", "poetry.lock", "pdm.lock", "uv.lock"); len(python) > 0 {
		if !slices.Contains(files, "uv.lock") {
			return Recipe{}, fmt.Errorf("found %s but no uv.lock: Python projects install with uv from a pyproject.toml and its uv.lock", strings.Join(python, ", "))
		}
		recipe, err := node(source, files)
		if recipe.Install != nil {
			recipe.Deploy.Build = slices.Concat(recipe.Build, [][]string{{"rm", "-rf", "node_modules"}})
		}
		recipe.Inputs = append(keep(files, pythonInput), recipe.Inputs...)
		recipe.Install = append([][]string{{"uv", "sync"}}, recipe.Install...)
		recipe.Deploy.Install = append([][]string{{"uv", "sync", "--locked", "--no-dev"}}, recipe.Deploy.Install...)
		recipe.Deploy.Freeze = runtimecontract.Freeze{Dependencies: []string{".venv"}}
		return recipe, err
	}
	if bun := present(files, "bun.lock", "bun.lockb", ".bun-version"); len(bun) > 0 {
		return Recipe{}, fmt.Errorf("found %s: Node.js projects install with npm or pnpm", bun[0])
	}
	if !slices.Contains(files, "package.json") {
		return Recipe{}, errors.New("found neither package.json nor pyproject.toml")
	}
	return node(source, files)
}

// Start is the command that serves an app from its server.py, server.ts or server.js entry once its recipe has run.
func Start(entry string) []string {
	if path.Ext(entry) == ".py" {
		return []string{".venv/bin/python", "-m", "holon.serve", entry}
	}
	return []string{"node_modules/.bin/holon-serve", entry}
}

type packageJSON struct {
	PackageManager string            `json:"packageManager"`
	Engines        map[string]string `json:"engines"`
	Scripts        map[string]string `json:"scripts"`
	DevEngines     struct {
		PackageManager declarations[struct{ Name string }] `json:"packageManager"`
		Runtime        declarations[nodeRuntime]           `json:"runtime"`
	} `json:"devEngines"`
}

type nodeRuntime struct{ Name, Version string }

type declarations[T any] []T

func (d *declarations[T]) UnmarshalJSON(data []byte) error {
	if data[0] == '{' {
		data = slices.Concat([]byte{'['}, data, []byte{']'})
	}
	return json.Unmarshal(data, (*[]T)(d))
}

func node(source fs.FS, files []string) (Recipe, error) {
	if !slices.Contains(files, "package.json") {
		return Recipe{}, nil
	}
	data, err := fs.ReadFile(source, "package.json")
	if err != nil {
		return Recipe{}, err
	}
	var manifest packageJSON
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Recipe{}, fmt.Errorf("package.json: %w", err)
	}
	declared := present(slices.Collect(maps.Keys(manifest.Engines)), "npm", "pnpm", "yarn", "bun")
	for _, manager := range manifest.DevEngines.PackageManager {
		declared = append(declared, manager.Name)
	}
	if manifest.PackageManager != "" {
		name, _, _ := strings.Cut(manifest.PackageManager, "@")
		declared = append(declared, name)
	}
	slices.Sort(declared)
	if declared = slices.Compact(declared); len(declared) > 1 {
		return Recipe{}, fmt.Errorf("package.json declares the package managers %s", strings.Join(declared, " and "))
	}
	manager := "npm"
	if len(present(files, "pnpm-lock.yaml", "pnpm-workspace.yaml")) > 0 {
		manager = "pnpm"
	}
	if len(declared) == 1 {
		manager = declared[0]
	}
	if !slices.Contains([]string{"npm", "pnpm"}, manager) {
		return Recipe{}, fmt.Errorf("package.json declares the package manager %s: Node.js projects install with npm or pnpm", manager)
	}
	versions := present(files, ".node-version", ".nvmrc")
	recipe := Recipe{
		NodeVersionNotice: manifest.nodeVersionNotice(versions),
		Inputs:            append(versions, keep(files, nodeInput)...), Install: [][]string{{manager, "install"}},
		Deploy: Deploy{Install: [][]string{{manager, "install"}}, Freeze: runtimecontract.Freeze{Dependencies: []string{"node_modules"}}},
	}
	if manifest.Scripts["build"] != "" {
		recipe.Build = [][]string{{manager, "run", "build"}}
	}
	recipe.Deploy.Build = recipe.Build
	if manager == "pnpm" {
		recipe.Deploy.Build = slices.Concat(recipe.Build, [][]string{{"rm", "-f", "node_modules/.modules.yaml", "node_modules/.pnpm-workspace-state-v1.json"}})
		if slices.Contains(files, "pnpm-lock.yaml") {
			recipe.Deploy.Install = [][]string{{"pnpm", "install", "--frozen-lockfile"}}
		}
	}
	return recipe, nil
}

func (manifest packageJSON) nodeVersionNotice(files []string) string {
	if len(files) > 0 {
		return ""
	}
	source, engine := "engines.node", strings.TrimSpace(manifest.Engines["node"])
	if engine == "" {
		source = "devEngines.runtime"
		var ranges []string
		for _, runtime := range manifest.DevEngines.Runtime {
			if runtime.Name == "node" {
				ranges = append(ranges, strings.TrimSpace(runtime.Version))
			}
		}
		if !slices.Contains(ranges, "") {
			engine = strings.Join(ranges, " || ")
		}
	}
	if engine == "" {
		return "is unspecified. A fresh install uses the newest LTS."
	}
	version := "v" + strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(engine, "=")), "v")
	if semver.Canonical(version)+semver.Build(version) == version {
		return ""
	}
	return fmt.Sprintf("follows %s %q. A fresh install can select a different matching version.", source, engine)
}

func present(files []string, names ...string) []string {
	return keep(names, func(name string) bool { return slices.Contains(files, name) })
}

func keep(names []string, wanted func(name string) bool) []string {
	return slices.DeleteFunc(slices.Clone(names), func(name string) bool { return !wanted(name) })
}

func pythonInput(name string) bool {
	return path.Base(name) == "pyproject.toml" || slices.Contains([]string{"uv.lock", "uv.toml", ".python-version"}, name)
}

func nodeInput(name string) bool {
	return strings.HasPrefix(name, "patches/") || slices.Contains([]string{
		"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "pnpm-workspace.yaml",
		"binding.gyp", ".npmrc", ".pnpmfile.cjs", "pnpmfile.cjs",
	}, path.Base(name))
}
