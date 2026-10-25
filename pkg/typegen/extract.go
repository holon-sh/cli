package typegen

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type extraction struct {
	Tools      []mcpcontract.Tool     `json:"tools"`
	Resources  []mcpcontract.Resource `json:"resources"`
	Diagnostic string                 `json:"diagnostic"`
	UsesApps   bool                   `json:"usesApps"`
	Sources    map[string]string      `json:"sources"`
}
type typeLanguage struct {
	runner          string
	programs, probe []string
}

var typeLanguages = map[string]typeLanguage{
	"typescript": {"runner.mjs", []string{"node"}, []string{"-e", "process.exit(Number(process.versions.node.split('.')[0]) < 22 ? 1 : 0)"}},
	"python":     {"runner.py", []string{"python3.15", "python3.14", "python3.13", "python3.12", "python3.11", "python3"}, []string{"-I", "-c", "import sys; sys.exit(sys.version_info < (3, 11))"}},
}

func (l typeLanguage) localProgram(ctx context.Context) (string, error) {
	for _, program := range l.programs {
		if exec.CommandContext(ctx, program, l.probe...).Run() == nil {
			return program, nil
		}
	}
	return "", fmt.Errorf("install a supported interpreter (%s)", strings.Join(l.programs, ", "))
}
func analysisEntry(root string) (string, string, error) {
	entry, err := sourceEntry(root)
	if err != nil {
		return "", "", err
	}
	if strings.HasSuffix(entry, ".py") {
		return entry, "python", nil
	}
	if isFile(filepath.Join(root, "package.json")) {
		// Browser-only apps also receive a slot for their own contract and dependencies.
		var pkg struct {
			Main    string            `json:"main"`
			Scripts map[string]string `json:"scripts"`
		}
		if err := readTypeJSON(filepath.Join(root, "package.json"), &pkg); err != nil {
			return "", "", err
		}
		if entry == "" {
			entry = pkg.Main
			if start := strings.Fields(pkg.Scripts["start"]); len(start) == 2 && slices.Contains([]string{"node", "tsx", "ts-node", "bun"}, start[0]) {
				entry = start[1]
			}
			if !filepath.IsLocal(entry) || !isFile(filepath.Join(root, entry)) {
				entry = ""
			}
		}
		return entry, "typescript", nil
	}
	return entry, "", nil
}
func extractTypes(ctx context.Context, root string, apps []App, o Options) (map[string]extraction, error) {
	results := map[string]extraction{}
	extract := o.Extract
	if extract == nil {
		env, err := commandenv.Environ(o.Env)
		if err != nil {
			return nil, err
		}
		extract = func(ctx context.Context, language, tooling, config, output string) error {
			return hostExtract(ctx, language, tooling, config, output, env, o)
		}
	}
	for language := range typeLanguages {
		var entries []map[string]string
		for _, app := range apps {
			if p := app.prepared; p != nil && p.language == language {
				entries = append(entries, map[string]string{"name": app.Name, "path": p.root, "entry": p.entry})
			}
		}
		if len(entries) == 0 {
			continue
		}
		config := filepath.Join(root, language+".json")
		output := filepath.Join(root, language+"-output.json")
		if err := writeJSON(config, map[string]any{"apps": entries}); err != nil {
			return nil, err
		}
		if err := extract(ctx, language, filepath.Join(root, "tooling"), config, output); err != nil {
			return nil, err
		}
		var found map[string]extraction
		if err := readTypeJSON(output, &found); err != nil {
			return nil, err
		}
		for name, result := range found {
			results[name] = result
		}
	}
	return results, nil
}

// hostExtract runs the extractor with the host's interpreter.
func hostExtract(ctx context.Context, language, tooling, config, output string, env []string, o Options) error {
	l := typeLanguages[language]
	program, err := l.localProgram(ctx)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, program, filepath.Join(tooling, language, l.runner), "extract", config, output)
	command.Env = env
	command.Stdout = o.Stderr
	command.Stderr = o.Stderr
	if err = command.Run(); err != nil {
		return fmt.Errorf("prepare MCP types with %s: %w", program, err)
	}
	return nil
}
func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// copyTree copies real package content into a checking snapshot, without retaining shared slots.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			info, err := os.Stat(real)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return copyTree(real, target)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
