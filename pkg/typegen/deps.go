package typegen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type packageJSON struct {
	PackageManager                                  string `json:"packageManager"`
	Dependencies, DevDependencies, PeerDependencies map[string]string
}

func (p packageJSON) declares(pkg string) bool {
	return p.Dependencies[pkg] != "" || p.DevDependencies[pkg] != "" || p.PeerDependencies[pkg] != ""
}

// packageManager is the project's declared package manager, else the one its nearest lockfile
// belongs to, else npm.
func packageManager(dir string, pkg packageJSON) string {
	if manager, _, _ := strings.Cut(pkg.PackageManager, "@"); manager != "" {
		return manager
	}
	for current := dir; ; current = filepath.Dir(current) {
		for _, lock := range []struct{ file, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"package-lock.json", "npm"}} {
			if isFile(filepath.Join(current, lock.file)) {
				return lock.manager
			}
		}
		if filepath.Dir(current) == current {
			return "npm"
		}
	}
}

func ensureNodeRuntime(ctx context.Context, dir, pkg string, o Options) (string, error) {
	const installCommand = "install"
	if slot, err := InstalledRuntime(dir, pkg); err == nil {
		return slot, nil
	}
	var manifest packageJSON
	if err := readTypeJSON(filepath.Join(dir, "package.json"), &manifest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%s has no package.json; create one and run npm install %s", dir, pkg)
		}
		return "", err
	}
	manager := packageManager(dir, manifest)
	if !manifest.declares(pkg) {
		verb := "add"
		if manager == "npm" {
			verb = installCommand
		}
		return "", fmt.Errorf("%s does not depend on %s; run %s %s %s", dir, pkg, manager, verb, pkg)
	}
	fmt.Fprintf(o.Stderr, "%s: %s %s\n", filepath.Base(dir), manager, installCommand)
	if err := validateEnv(o.Env); err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, manager, installCommand)
	command.Dir = dir
	command.Env = append(command.Environ(), o.Env...)
	command.Stdout, command.Stderr = o.Stderr, o.Stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s %s in %s: %w", manager, installCommand, dir, err)
	}
	return InstalledRuntime(dir, pkg)
}

// DeclaredTargets are the slots a project declares: ts when package.json depends on holon-sh,
// py when pyproject.toml or requirements declare holon-sh. None is an error.
func DeclaredTargets(dir string) ([]string, error) {
	var targets []string
	var manifest packageJSON
	if err := readTypeJSON(filepath.Join(dir, "package.json"), &manifest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if manifest.declares(slotPackages[TargetTS]) {
		targets = append(targets, TargetTS)
	}
	if ensurePythonRuntime(dir) == nil {
		targets = append(targets, TargetPy)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("%s declares no runtime: depend on holon-sh in package.json, or on holon-sh in pyproject.toml", dir)
	}
	return targets, nil
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
