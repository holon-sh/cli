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

	"github.com/holon-sh/cli/internal/commandenv"
)

type packageJSON struct {
	PackageManager                                  string `json:"packageManager"`
	Dependencies, DevDependencies, PeerDependencies map[string]string
}

func (p packageJSON) declares(pkg string) bool {
	return p.Dependencies[pkg] != "" || p.DevDependencies[pkg] != "" || p.PeerDependencies[pkg] != ""
}

// packageManager is the project's declared package manager, else the one its nearest lockfile
// belongs to, else npm. The second value is that manager's verb for adding a dependency.
func packageManager(dir string, pkg packageJSON) (string, string) {
	manager, _, _ := strings.Cut(pkg.PackageManager, "@")
	if manager == "" {
		manager = "npm"
		for current := dir; ; current = filepath.Dir(current) {
			found := false
			for _, lock := range []struct{ file, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"package-lock.json", "npm"}} {
				if isFile(filepath.Join(current, lock.file)) {
					manager = lock.manager
					found = true
					break
				}
			}
			if found || filepath.Dir(current) == current {
				break
			}
		}
	}
	if manager == "npm" {
		return manager, "install"
	}
	return manager, "add"
}

// ensureNodeRuntime returns the installed slot package for dir. A package that package.json
// declares but node_modules lacks is installed with the project's package manager; an undeclared
// one is an error naming the command that adds it.
func ensureNodeRuntime(ctx context.Context, dir, pkg string, o Options) (string, error) {
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
	manager, verb := packageManager(dir, manifest)
	if !manifest.declares(pkg) {
		return "", fmt.Errorf("%s does not depend on %s; run %s %s %s", dir, pkg, manager, verb, pkg)
	}
	if err := run(ctx, dir, manager, []string{"install"}, o); err != nil {
		return "", err
	}
	return InstalledRuntime(dir, pkg)
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

func run(ctx context.Context, dir, program string, args []string, o Options) error {
	fmt.Fprintf(o.Stderr, "%s: %s %s\n", filepath.Base(dir), program, strings.Join(args, " "))
	env, err := commandenv.Environ(o.Env)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, program, args...)
	command.Dir = dir
	command.Env = env
	command.Stdout = o.Stderr
	command.Stderr = o.Stderr
	if err = command.Run(); err != nil {
		return fmt.Errorf("%s %s in %s: %w", program, strings.Join(args, " "), dir, err)
	}
	return nil
}
