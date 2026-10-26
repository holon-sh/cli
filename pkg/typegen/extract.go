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
	"python":     {[]string{"extract.py"}, []string{"runner.py", "check"}, []string{"python3.15", "python3.14", "python3.13", "python3.12", "python3.11", "python3"}, []string{"-I", "-c", "import sys; sys.exit(sys.version_info < (3, 11))"}},
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

// extractionEntry is one snapshot to extract: the runner reads entry under path.
type extractionEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Entry string `json:"entry"`
}

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
			if err := validateEnv(o.Env); err != nil {
				return nil, err
			}
			l := typeLanguages[language]
			program, err := l.localProgram(ctx)
			if err != nil {
				return nil, err
			}
			command := l.command(ctx, program, tooling, language, l.extractor, config, output)
			command.Env = append(command.Environ(), o.Env...)
			command.Stdout, command.Stderr = o.Stderr, o.Stderr
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
