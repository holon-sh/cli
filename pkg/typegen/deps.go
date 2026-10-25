package typegen

import (
	"context"
	"fmt"
	"github.com/holon-sh/cli/internal/commandenv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type packageJSON struct {
	PackageManager                                  string `json:"packageManager"`
	Dependencies, DevDependencies, PeerDependencies map[string]string
}

func (p packageJSON) declares(pkg string) bool {
	return p.Dependencies[pkg] != "" || p.DevDependencies[pkg] != "" || p.PeerDependencies[pkg] != ""
}

func (p packageJSON) kinds(imported map[string]bool) []string {
	var kinds []string
	if p.declares(slotPackages["browser"]) || imported[slotPackages["browser"]] {
		kinds = append(kinds, "browser")
	}
	if p.declares(slotPackages["typescript"]) || imported[slotPackages["typescript"]] || len(kinds) == 0 {
		kinds = append(kinds, "typescript")
	}
	return kinds
}

func ensureDependencies(ctx context.Context, app App, imports []string, o Options) error {
	if isFile(filepath.Join(app.Path, "package.json")) {
		var pkg packageJSON
		if err := readTypeJSON(filepath.Join(app.Path, "package.json"), &pkg); err != nil {
			return err
		}
		imported := map[string]bool{}
		for _, specifier := range imports {
			name, _, _ := strings.Cut(specifier, "/")
			imported[name] = true
		}
		legacy := slices.Contains(imports, "holon-sh/browser") || slices.Contains(imports, "holon-sh/browser-runtime")
		if legacy {
			return fmt.Errorf("%s imports holon-sh/browser, which moved to the holon-ui package; depend on holon-ui and import from \"holon-ui\"", app.Name)
		}
		var missing []string
		for _, kind := range pkg.kinds(imported) {
			runtime := slotPackages[kind]
			if !pkg.declares(runtime) {
				missing = append(missing, runtime)
			}
		}
		if len(missing) > 0 {
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
			if err := installDependency(ctx, app.Path, manager, append([]string{verb}, missing...), o); err != nil {
				return err
			}
		}
	}
	_, language, err := analysisEntry(app.Path)
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
