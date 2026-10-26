package typegen

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/pkg/tooling/core"
)

type extraction = core.Result
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
type extractionEntry = core.App

// extractTypes runs one extractor batch per language under root and returns the results by name.
func extractTypes(ctx context.Context, root string, batches map[string][]extractionEntry, o Options) (map[string]extraction, error) {
	results := map[string]extraction{}
	for _, language := range slices.Sorted(maps.Keys(batches)) {
		tooling := filepath.Join(root, "tooling")
		var found map[string]extraction
		if o.Extract != nil {
			config, output := filepath.Join(root, language+"-extract.json"), filepath.Join(root, language+"-extract-output.json")
			if err := writeJSON(config, map[string]any{"apps": batches[language]}); err != nil {
				return nil, err
			}
			if err := o.Extract(ctx, language, tooling, config, output); err != nil {
				return nil, err
			}
			if err := readTypeJSON(output, &found); err != nil {
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
			runner := core.Runner{Language: language, Program: program, Path: filepath.Join(tooling, language, l.runner), Env: env, Stdout: o.Stderr, Stderr: o.Stderr}
			found, err = runner.Extract(ctx, batches[language])
			if err != nil {
				return nil, fmt.Errorf("prepare MCP types with %s: %w", program, err)
			}
		}
		for _, app := range batches[language] {
			result, ok := found[app.Name]
			if !ok {
				return nil, fmt.Errorf("extractor did not return %s", app.Name)
			}
			if err := result.Validate(ctx); err != nil {
				return nil, fmt.Errorf("%s: %w", app.Name, err)
			}
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
