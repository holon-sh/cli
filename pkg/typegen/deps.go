package typegen

import (
	"context"
	"fmt"
	"github.com/holon-sh/cli/internal/commandenv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func ensureDependencies(ctx context.Context, app App, o Options) error {
	if isFile(filepath.Join(app.Path, "package.json")) {
		var pkg struct {
			PackageManager                                  string `json:"packageManager"`
			Dependencies, DevDependencies, PeerDependencies map[string]string
		}
		if err := readTypeJSON(filepath.Join(app.Path, "package.json"), &pkg); err != nil {
			return err
		}
		hasRuntime := false
		for _, dependencies := range []map[string]string{pkg.Dependencies, pkg.DevDependencies, pkg.PeerDependencies} {
			for _, name := range nodePackages {
				if dependencies[name] != "" {
					hasRuntime = true
				}
			}
		}
		if !hasRuntime {
			manager, _, _ := strings.Cut(pkg.PackageManager, "@")
			if manager == "" {
				manager = "npm"
				for dir := app.Path; ; dir = filepath.Dir(dir) {
					found := false
					for _, lock := range []struct{ file, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"package-lock.json", "npm"}} {
						if isFile(filepath.Join(dir, lock.file)) {
							manager = lock.manager
							found = true
							break
						}
					}
					if found || filepath.Dir(dir) == dir {
						break
					}
				}
			}
			verb := "add"
			switch manager {
			case "npm":
				verb = "install"
			case "pnpm", "yarn", "bun":
			default:
				return fmt.Errorf("unsupported package manager %q", manager)
			}
			if err := installDependency(ctx, app.Path, manager, []string{verb, nodePackage}, o); err != nil {
				return err
			}
		}
	}
	entry, language, err := analysisEntry(app.Path)
	_ = entry
	if err != nil {
		return err
	}
	if language != "python" {
		return nil
	}
	found := false
	hasPyproject := isFile(filepath.Join(app.Path, "pyproject.toml"))
	var project string
	for _, name := range []string{"pyproject.toml", "requirements.txt", "requirements.in"} {
		data, _ := os.ReadFile(filepath.Join(app.Path, name))
		if name == "pyproject.toml" {
			project = string(data)
		}
		if regexp.MustCompile(`(?im)(?:^|["'\s])holon[-_]sh(?:\[|[\s"'<=>!~;]|$)`).Match(data) {
			found = true
		}
	}
	if found {
		return nil
	}
	if isFile(filepath.Join(app.Path, "uv.lock")) || hasPyproject && !strings.Contains(project, "[tool.poetry") {
		return installDependency(ctx, app.Path, "uv", []string{"add", "holon-sh"}, o)
	}
	if strings.Contains(project, "[tool.poetry") {
		return installDependency(ctx, app.Path, "poetry", []string{"add", "holon-sh"}, o)
	}
	fmt.Fprintf(o.Stdout, "%s: install runtime with pip install holon-sh and add holon-sh to your requirements\n", app.Name)
	return nil
}
func installDependency(ctx context.Context, root, program string, args []string, o Options) error {
	fmt.Fprintf(o.Stdout, "%s: %s %s\n", filepath.Base(root), program, strings.Join(args, " "))
	if o.NoInstall {
		return nil
	}
	env, err := commandenv.Environ(append(append([]string{}, o.Env...), "HOLON_SKIP_POSTINSTALL=1"))
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, program, args...)
	command.Dir = root
	command.Env = env
	command.Stdout = o.Stdout
	command.Stderr = o.Stderr
	if err = command.Run(); err != nil {
		return fmt.Errorf("install runtime in %s: %w", root, err)
	}
	return nil
}
