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
	PackageManager                                                        string `json:"packageManager"`
	Dependencies, DevDependencies, PeerDependencies, OptionalDependencies map[string]string
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

// Install installs missing declared SDK runtimes with the project's package manager.
// Call it before snapshotting sources: package installation can create or update lockfiles.
func Install(ctx context.Context, consumers []Consumer, options Options) error {
	o := options.defaults()
	for _, c := range consumers {
		if err := c.validate(); err != nil {
			return err
		}
		for _, target := range c.Targets {
			var err error
			switch target {
			case TargetTS:
				err = ensureNodeRuntime(ctx, c.Dir, o)
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

func ensureNodeRuntime(ctx context.Context, dir string, o Options) error {
	const installCommand = "install"
	if _, err := InstalledRuntime(dir); err == nil {
		return nil
	}
	var manifest packageJSON
	if err := readTypeJSON(filepath.Join(dir, "package.json"), &manifest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s has no package.json; create one and run npm install %s", dir, nodePackage)
		}
		return err
	}
	manager := packageManager(dir, manifest)
	if !manifest.declares(nodePackage) {
		verb := "add"
		if manager == "npm" {
			verb = installCommand
		}
		return fmt.Errorf("%s does not depend on %s; run %s %s %s", dir, nodePackage, manager, verb, nodePackage)
	}
	fmt.Fprintf(o.Stderr, "%s: %s %s\n", filepath.Base(dir), manager, installCommand)
	if err := validateEnv(o.Env); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, manager, installCommand)
	command.Dir = dir
	command.Env = append(append(command.Environ(), o.Env...), "HOLON_SKIP_POSTINSTALL=1")
	command.Stdout, command.Stderr = o.Stderr, o.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s %s in %s: %w", manager, installCommand, dir, err)
	}
	_, err := InstalledRuntime(dir)
	return err
}

// ErrNoRuntime identifies a project without a declared Holon SDK runtime.
var ErrNoRuntime = errors.New("declares no runtime: depend on holon-sh in package.json, on holon-sh in pyproject.toml, or on github.com/holon-sh/sdk/go in go.mod")

// DeclaredTargets are the slots a project declares: ts when package.json depends on holon-sh,
// py when pyproject.toml or requirements declare holon-sh, and go when go.mod requires the Go SDK. None is an error.
func DeclaredTargets(dir string) ([]string, error) {
	var targets []string
	var manifest packageJSON
	if err := readTypeJSON(filepath.Join(dir, "package.json"), &manifest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if manifest.declares(nodePackage) {
		targets = append(targets, TargetTS)
	}
	if ensurePythonRuntime(dir) == nil {
		targets = append(targets, TargetPy)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && goRuntime.Match(data) {
		targets = append(targets, TargetGo)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("%s %w", dir, ErrNoRuntime)
	}
	return targets, nil
}

var goRuntime = regexp.MustCompile(`(?m)^\s*(?:require\s+)?github\.com/holon-sh/sdk/go\s+v[^\s]+`)

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
