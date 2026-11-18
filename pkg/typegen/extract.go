package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/holon-sh/protocol/runtimecontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Served is what a running server answers to initialize and to each catalog list. An installation serving an
// OpenAPI document adds the Responses the document's response schemas compile to, as [mcpcontract.Contract] has them.
type Served struct {
	ServerInfo   json.RawMessage         `json:"serverInfo"`
	Capabilities *mcp.ServerCapabilities `json:"capabilities"`
	Lists        map[string]ServedList   `json:"lists"`
	Responses    json.RawMessage         `json:"responses,omitempty"`
}

type ServedList struct {
	Pages []json.RawMessage `json:"pages"`
	Error string            `json:"error,omitempty"`
}

func (s *Served) pages(method string) ([]json.RawMessage, error) {
	list := s.Lists[method]
	if list.Error != "" {
		return nil, errors.New(list.Error)
	}
	return list.Pages, nil
}

// RunnerError is a contract runner's failure: the status it exited with and what it wrote to
// stderr. Status 2 is a diagnostic; status 3 means the server stopped on an uninstalled package,
// named on the last line.
type RunnerError struct {
	Status int
	Stderr string
}

func (e *RunnerError) Error() string {
	return fmt.Sprintf("contract runner exited with status %d: %s", e.Status, strings.TrimSpace(e.Stderr))
}

// serverTimeout is how long a server may take to answer.
var serverTimeout = 30 * time.Second

// typeLanguage is how one language's tooling runs: the contract runner's interpreter flags and
// script, the checker script, relative to the language's tooling directory, and the host
// interpreters that can run them.
type typeLanguage struct {
	flags           []string
	runner, checker string
	programs, probe []string
}

var typeLanguages = map[string]typeLanguage{
	"typescript": {[]string{"--experimental-vm-modules", "--no-warnings"}, "runner.mjs", "check.mjs", []string{"node"}, []string{"-e", "process.exit(Number(process.versions.node.split('.')[0]) < 22 ? 1 : 0)"}},
	"python":     {nil, "extract.py", "runner.py", []string{"python3", "python3.15", "python3.14", "python3.13", "python3.12", "python3.11"}, []string{"-I", "-c", "import sys; sys.exit(sys.version_info < (3, 11))"}},
}

// RunnerCommand is the interpreter arguments that run language's contract runner from dir, its tooling directory.
func RunnerCommand(language, dir string) []string {
	return append(slices.Clone(typeLanguages[language].flags), filepath.Join(dir, typeLanguages[language].runner))
}

// Checker is language's checker script, in its tooling directory.
func Checker(language string) string { return typeLanguages[language].checker }

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
// holon types generate --contract prints for it. It stands in for running the server when a
// package the server imports is not installed.
const ContractFile = "holon-contract.json"

// missingPackage is a runner's report that the server stopped on an uninstalled package.
type missingPackage struct{ name, diagnostic string }

func (m *missingPackage) Error() string { return m.name + " is not installed" }

// extractSources obtains each non-Go path source's contract by running its server, or from its
// committed ContractFile when a package the server imports is not installed. With drift, a
// committed contract that differs from what the server answers is an error.
func extractSources(ctx context.Context, root string, sources []*preparedSource, o Options, drift bool) error {
	var run []*preparedSource
	committed := map[*preparedSource]*Document{}
	for _, s := range sources {
		if s.language == "go" || s.metadata.Schema != nil {
			continue
		}
		document, err := s.committedContract()
		if err != nil {
			return err
		}
		committed[s] = document
		run = append(run, s)
	}
	if len(run) != 0 {
		if err := tooling.Unpack(filepath.Join(root, "tooling")); err != nil {
			return err
		}
	}
	results := make([]*Served, len(run))
	failures := make([]error, len(run))
	var group sync.WaitGroup
	for i, s := range run {
		group.Go(func() { results[i], failures[i] = runServer(ctx, filepath.Join(root, "tooling"), s, o) })
	}
	group.Wait()
	for i, s := range run {
		document := committed[s]
		var missing *missingPackage
		if errors.As(failures[i], &missing) {
			if document == nil {
				return fmt.Errorf("%s: %s is not installed; install the application's dependencies, or commit its contract (holon types generate --contract %s -o %s)\n%s", s.origin, missing.name, s.origin, filepath.Join(s.origin, ContractFile), missing.diagnostic)
			}
			s.metadata.Identity, s.metadata.Schema = document.Identity, document.Schema
			continue
		}
		if failures[i] != nil {
			return failures[i]
		}
		if err := s.extract(results[i]); err != nil {
			return err
		}
		if drift && document != nil {
			same, err := mcptype.SameContract(s.metadata.Schema, document.Schema)
			if err != nil {
				return err
			}
			if !same || document.Identity != s.metadata.Identity {
				contract := filepath.Join(s.origin, ContractFile)
				return fmt.Errorf("%s is stale: the server answers a different contract; regenerate it with holon types generate --contract %s -o %s", contract, s.origin, contract)
			}
		}
	}
	return nil
}

// committedContract reads the source's ContractFile, or returns nil when it has none.
func (s *preparedSource) committedContract() (*Document, error) {
	path := filepath.Join(s.origin, ContractFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	document, err := ParseDocument(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if document.Schema == nil {
		return nil, fmt.Errorf("%s has no contract", path)
	}
	if s.metadata.Identity != (mcpcontract.Identity{}) && document.Identity != s.metadata.Identity {
		return nil, fmt.Errorf("%s describes %s@%s but server.json names %s@%s", path, document.Identity.Name, document.Identity.Version, s.metadata.Identity.Name, s.metadata.Identity.Version)
	}
	return document, nil
}

func runServer(ctx context.Context, tooling string, s *preparedSource, o Options) (*Served, error) {
	extensions, _ := json.Marshal(clientExtensions)
	args := append([]string{"contract", s.origin, s.entry, string(extensions)}, runtimecontract.CatalogMethods...)
	var stdout bytes.Buffer
	var err error
	if o.Run != nil {
		err = o.Run(ctx, s.language, tooling, args, &stdout)
	} else {
		err = runLocal(ctx, tooling, s.language, args, &stdout, o.Env)
	}
	var failed *RunnerError
	switch {
	case errors.As(err, &failed) && failed.Status == 3:
		lines := strings.Split(strings.TrimRight(failed.Stderr, "\n"), "\n")
		return nil, &missingPackage{name: lines[len(lines)-1], diagnostic: strings.Join(lines[:len(lines)-1], "\n")}
	case errors.As(err, &failed) && failed.Status == 2:
		return nil, fmt.Errorf("%s: %s", s.origin, strings.TrimSpace(failed.Stderr))
	case err != nil:
		return nil, fmt.Errorf("%s: %w", s.origin, err)
	}
	result := &Served{}
	if err := json.Unmarshal(stdout.Bytes(), result); err != nil {
		return nil, fmt.Errorf("%s: the contract runner printed no answers: %w", s.origin, err)
	}
	return result, nil
}

func runLocal(ctx context.Context, tooling, language string, args []string, stdout io.Writer, env []string) error {
	l := typeLanguages[language]
	run := func(program string) error {
		limited, cancel := context.WithTimeout(ctx, serverTimeout)
		defer cancel()
		command := exec.CommandContext(limited, program, slices.Concat(RunnerCommand(language, filepath.Join(tooling, language)), args)...)
		command.Env = commandenv.Environ(env)
		var stderr bytes.Buffer
		command.Stdout, command.Stderr = stdout, &stderr
		err := command.Run()
		if errors.Is(limited.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return fmt.Errorf("%s: no catalog within %s", args[2], serverTimeout)
		}
		if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
			return &RunnerError{Status: exit.ExitCode(), Stderr: stderr.String()}
		}
		return err
	}
	if language == "python" {
		if own := pythonEnvironment(args[1]); own != "" && exec.CommandContext(ctx, own, l.probe...).Run() == nil {
			if err := run(own); !lacksSchemaLibraries(err) {
				return err
			}
		}
	}
	program, err := l.localProgram(ctx)
	if err != nil {
		return err
	}
	return run(program)
}

// lacksSchemaLibraries reports a contract runner that stopped on an uninstalled mcp or pydantic.
func lacksSchemaLibraries(err error) bool {
	var failed *RunnerError
	return errors.As(err, &failed) && failed.Status == 3 && (strings.HasSuffix(failed.Stderr, "\nmcp\n") || strings.HasSuffix(failed.Stderr, "\npydantic\n"))
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
