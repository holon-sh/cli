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

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

type extraction struct {
	Tools      []mcpcontract.Tool     `json:"tools"`
	Resources  []mcpcontract.Resource `json:"resources"`
	Diagnostic string                 `json:"diagnostic"`
	Sources    map[string]string      `json:"sources"`
}

// typeLanguage is how one language's tooling runs: the extractor and checker commands, relative to
// the language's tooling directory, and the host interpreters that can run them.
type typeLanguage struct {
	extractor, checker []string
	programs, probe    []string
}

var typeLanguages = map[string]typeLanguage{
	"typescript": {[]string{"runner.mjs", "extract"}, []string{"runner.mjs", "check"}, []string{"node"}, []string{"-e", "process.exit(Number(process.versions.node.split('.')[0]) < 22 ? 1 : 0)"}},
	"python":     {[]string{"extract.py"}, []string{"runner.py", "check"}, []string{"python3", "python3.15", "python3.14", "python3.13", "python3.12", "python3.11"}, []string{"-I", "-c", "import sys; sys.exit(sys.version_info < (3, 11))"}},
}

// command runs step, one of the language's tooling commands, unpacked under tooling, with args.
func (l typeLanguage) command(ctx context.Context, program, tooling, language string, step []string, args ...string) *exec.Cmd {
	arguments := append([]string{filepath.Join(tooling, language, step[0])}, step[1:]...)
	return exec.CommandContext(ctx, program, append(arguments, args...)...)
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

// extractionEntry is one source to extract: the runner imports entry under path.
type extractionEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Entry string `json:"entry"`
}

func extractTypes(ctx context.Context, root string, batches map[string][]extractionEntry, o Options) (map[string]extraction, error) {
	results := map[string]extraction{}
	tooling := filepath.Join(root, "tooling")
	for _, language := range slices.Sorted(maps.Keys(batches)) {
		if o.Extract != nil {
			config := filepath.Join(root, language+"-extract.json")
			output := filepath.Join(root, language+"-extract-output.json")
			if err := writeJSON(config, map[string]any{"apps": batches[language]}); err != nil {
				return nil, err
			}
			if err := o.Extract(ctx, language, tooling, config, output); err != nil {
				return nil, err
			}
			if err := readTypeJSON(output, &results); err != nil {
				return nil, err
			}
			continue
		}
		if err := validateEnv(o.Env); err != nil {
			return nil, err
		}
		l := typeLanguages[language]
		host, err := l.localProgram(ctx)
		if err != nil {
			return nil, err
		}
		// The runners import each server, so a Python server runs in its own environment.
		programs := map[string][]extractionEntry{}
		for _, entry := range batches[language] {
			program := host
			if language == "python" {
				program = pythonEnvironment(entry.Path, host)
			}
			programs[program] = append(programs[program], entry)
		}
		for i, program := range slices.Sorted(maps.Keys(programs)) {
			config := filepath.Join(root, fmt.Sprintf("%s-extract-%d.json", language, i))
			output := filepath.Join(root, fmt.Sprintf("%s-extract-output-%d.json", language, i))
			if err := writeJSON(config, map[string]any{"apps": programs[program]}); err != nil {
				return nil, err
			}
			command := l.command(ctx, program, tooling, language, l.extractor, config, output)
			command.Env = append(append(command.Environ(), o.Env...), "PYTHONDONTWRITEBYTECODE=1")
			command.Stdout, command.Stderr = o.Stderr, o.Stderr
			if err = command.Run(); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, fmt.Errorf("prepare MCP types with %s: %w", program, err)
			}
			if err := readTypeJSON(output, &results); err != nil {
				return nil, err
			}
		}
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
