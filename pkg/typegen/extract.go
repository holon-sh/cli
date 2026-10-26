package typegen

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"io/fs"
	"maps"
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
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", fmt.Errorf("install a supported interpreter (%s)", strings.Join(l.programs, ", "))
}

// extractionEntry is one snapshot to extract: the runner reads entry under path.
type extractionEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Entry string `json:"entry"`
}

// extractTypes runs one extractor batch per language under root and returns the results by name.
func extractTypes(ctx context.Context, root string, batches map[string][]extractionEntry, o Options) (map[string]extraction, error) {
	results := map[string]extraction{}
	for _, language := range slices.Sorted(maps.Keys(batches)) {
		config := filepath.Join(root, language+"-extract.json")
		output := filepath.Join(root, language+"-extract-output.json")
		tooling := filepath.Join(root, "tooling")
		if err := writeJSON(config, map[string]any{"apps": batches[language]}); err != nil {
			return nil, err
		}
		if o.Extract != nil {
			if err := o.Extract(ctx, language, tooling, config, output); err != nil {
				return nil, err
			}
		} else {
			env, err := commandenv.Environ(o.Env)
			if err != nil {
				return nil, err
			}
			l := typeLanguages[language]
			program, err := l.localProgram(ctx)
			if err != nil {
				return nil, err
			}
			command := exec.CommandContext(ctx, program, filepath.Join(tooling, language, l.runner), "extract", config, output)
			command.Env, command.Stdout, command.Stderr = env, o.Stderr, o.Stderr
			if err = command.Run(); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, fmt.Errorf("prepare MCP types with %s: %w", program, err)
			}
		}
		var found map[string]extraction
		if err := readTypeJSON(output, &found); err != nil {
			return nil, err
		}
		maps.Copy(results, found)
	}
	return results, nil
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
