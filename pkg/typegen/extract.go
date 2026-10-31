package typegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/opencontainers/go-digest"
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

// ContractFile is a server's committed contract document, beside its server.json: what
// holon types generate --contract prints for it. It stands in for running the server when the
// server's dependencies are not installed.
const ContractFile = "holon-contract.json"

// extractSources obtains each non-Go path source's contract by running its server, or from its
// committed ContractFile when the server's dependencies are not installed. With drift, a committed
// contract that differs from what the server answers is an error.
func extractSources(ctx context.Context, root string, sources []*preparedSource, o Options, drift bool) error {
	batches := map[string][]extractionEntry{}
	committed := map[*preparedSource]*Document{}
	var run []*preparedSource
	for _, s := range sources {
		if s.language == "go" {
			continue
		}
		document, err := s.committedContract(ctx)
		if err != nil {
			return err
		}
		// An extraction hook decides for itself where and how servers run.
		if o.Extract == nil && !s.runnable(ctx) {
			if document == nil {
				return fmt.Errorf("%s: cannot run the server because its dependencies are not installed; install them, or commit its contract (holon types generate --contract %s -o %s)", s.origin, s.origin, filepath.Join(s.origin, ContractFile))
			}
			s.metadata.Schema = document.Schema
			s.sources = map[string]string{ContractFile: digest.FromBytes(document.Data).Encoded()}
			continue
		}
		if drift && document != nil {
			committed[s] = document
		}
		batches[s.language] = append(batches[s.language], extractionEntry{Name: s.name, Path: s.origin, Entry: s.entry})
		run = append(run, s)
	}
	results, err := extractTypes(ctx, root, batches, o)
	if err != nil {
		return err
	}
	for _, s := range run {
		if err := s.extract(results[s.name]); err != nil {
			return err
		}
		if document := committed[s]; document != nil {
			answered, err := NewDocument(ctx, &mcpcontract.Metadata{Identity: s.metadata.Identity, Schema: s.metadata.Schema})
			if err != nil {
				return err
			}
			recorded, err := NewDocument(ctx, &mcpcontract.Metadata{Identity: document.Identity, Schema: document.Schema})
			if err != nil {
				return err
			}
			if answered.Digest != recorded.Digest {
				return fmt.Errorf("%s is stale: the server answers a different contract; regenerate it with holon types generate --contract %s -o %[1]s", filepath.Join(s.origin, ContractFile), s.origin)
			}
		}
	}
	return nil
}

// committedContract reads the source's ContractFile, or returns nil when it has none.
func (s *preparedSource) committedContract(ctx context.Context) (*Document, error) {
	path := filepath.Join(s.origin, ContractFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	document, err := ParseDocument(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if document.Schema == nil {
		return nil, fmt.Errorf("%s has no contract", path)
	}
	if document.Identity != s.metadata.Identity {
		return nil, fmt.Errorf("%s describes %s@%s but server.json names %s@%s", path, document.Identity.Name, document.Identity.Version, s.metadata.Identity.Name, s.metadata.Identity.Version)
	}
	return document, nil
}

// runnable reports whether the source's server can be imported here: a TypeScript server needs
// its installed MCP SDK, a Python server an interpreter that imports mcp.
func (s *preparedSource) runnable(ctx context.Context) bool {
	if s.language == "typescript" {
		for dir := s.origin; ; dir = filepath.Dir(dir) {
			if info, err := os.Stat(filepath.Join(dir, "node_modules", "@modelcontextprotocol", "server")); err == nil && info.IsDir() {
				return true
			}
			if filepath.Dir(dir) == dir {
				return false
			}
		}
	}
	host, err := typeLanguages["python"].localProgram(ctx)
	return err == nil && exec.CommandContext(ctx, pythonEnvironment(s.origin, host), "-c", "import mcp.server").Run() == nil
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
