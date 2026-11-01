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

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/opencontainers/go-digest"
)

// extraction is the contract a runner prints: the server's catalog and the application files it
// loaded, with their SHA-256 digests.
type extraction struct {
	Tools     []mcpcontract.Tool     `json:"tools"`
	Resources []mcpcontract.Resource `json:"resources"`
	Sources   map[string]string      `json:"sources"`
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
// script, the checker command, relative to the language's tooling directory, and the host
// interpreters that can run them.
type typeLanguage struct {
	flags           []string
	runner          string
	checker         []string
	programs, probe []string
}

var typeLanguages = map[string]typeLanguage{
	"typescript": {[]string{"--experimental-vm-modules", "--no-warnings"}, "runner.mjs", []string{"runner.mjs", "check"}, []string{"node"}, []string{"-e", "process.exit(Number(process.versions.node.split('.')[0]) < 22 ? 1 : 0)"}},
	"python":     {nil, "extract.py", []string{"runner.py", "check"}, []string{"python3", "python3.15", "python3.14", "python3.13", "python3.12", "python3.11"}, []string{"-I", "-c", "import sys; sys.exit(sys.version_info < (3, 11))"}},
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
	directories := map[*preparedSource]string{}
	for _, s := range sources {
		if s.language == "go" {
			continue
		}
		document, err := s.committedContract(ctx)
		if err != nil {
			return err
		}
		committed[s], directories[s] = document, s.origin
		if o.Run != nil {
			// A hook may run servers where only root is visible, such as a container without network
			// access, so it gets a snapshot there: a server that needs only its schema libraries
			// extracts with the tooling's copies, and others report the package they miss.
			directories[s] = filepath.Join(root, "sources", s.name)
			fingerprint, err := SnapshotSource(s.name, s.origin, directories[s])
			if err != nil {
				return err
			}
			if fingerprint != s.fingerprint {
				return fmt.Errorf("%s changed during preparation; discarding the superseded update", s.origin)
			}
		}
		run = append(run, s)
	}
	results := make([]*extraction, len(run))
	failures := make([]error, len(run))
	var group sync.WaitGroup
	for i, s := range run {
		group.Go(func() { results[i], failures[i] = runServer(ctx, filepath.Join(root, "tooling"), directories[s], s, o) })
	}
	group.Wait()
	for i, s := range run {
		document := committed[s]
		var missing *missingPackage
		if errors.As(failures[i], &missing) {
			if document == nil {
				return fmt.Errorf("%s: %s is not installed; install the application's dependencies, or commit its contract (holon types generate --contract %s -o %s)\n%s", s.origin, missing.name, s.origin, filepath.Join(s.origin, ContractFile), missing.diagnostic)
			}
			s.metadata.Schema = document.Schema
			s.sources = map[string]string{ContractFile: digest.FromBytes(document.Data).Encoded()}
			continue
		}
		if failures[i] != nil {
			return failures[i]
		}
		if err := s.extract(results[i]); err != nil {
			return err
		}
		if drift && document != nil {
			answered, err := NewDocument(ctx, &mcpcontract.Metadata{Identity: s.metadata.Identity, Schema: s.metadata.Schema})
			if err != nil {
				return err
			}
			recorded, err := NewDocument(ctx, &mcpcontract.Metadata{Identity: document.Identity, Schema: document.Schema})
			if err != nil {
				return err
			}
			if answered.Digest != recorded.Digest {
				contract := filepath.Join(s.origin, ContractFile)
				return fmt.Errorf("%s is stale: the server answers a different contract; regenerate it with holon types generate --contract %s -o %s", contract, s.origin, contract)
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

// runServer runs the source's server, in dir, through its language's contract runner and parses
// the contract it prints.
func runServer(ctx context.Context, tooling, dir string, s *preparedSource, o Options) (*extraction, error) {
	args := []string{"contract", dir, s.entry}
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
	result := &extraction{}
	if err := json.Unmarshal(stdout.Bytes(), result); err != nil {
		return nil, fmt.Errorf("%s: the contract runner printed no contract: %w", s.origin, err)
	}
	return result, nil
}

// runLocal runs a contract runner with the host's interpreter. A Python server (args[1]) runs in its
// own environment when that imports mcp and pydantic.
func runLocal(ctx context.Context, tooling, language string, args []string, stdout io.Writer, env []string) error {
	if err := validateEnv(env); err != nil {
		return err
	}
	l := typeLanguages[language]
	program, err := l.localProgram(ctx)
	if err != nil {
		return err
	}
	if language == "python" {
		if program = pythonProgram(ctx, args[1], program); program == "" {
			return &RunnerError{Status: 3, Stderr: "no Python environment here imports mcp and pydantic\nmcp\n"}
		}
	}
	limited, cancel := context.WithTimeout(ctx, serverTimeout)
	defer cancel()
	command := exec.CommandContext(limited, program, slices.Concat(l.flags, []string{filepath.Join(tooling, language, l.runner)}, args)...)
	command.Env = append(command.Environ(), env...)
	var stderr bytes.Buffer
	command.Stdout, command.Stderr = stdout, &stderr
	err = command.Run()
	if errors.Is(limited.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		return fmt.Errorf("%s: no catalog within %s", args[2], serverTimeout)
	}
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
		return &RunnerError{Status: exit.ExitCode(), Stderr: stderr.String()}
	}
	return err
}

// pythonProgram is the interpreter that runs a Python server: its own environment's when that
// imports mcp and pydantic, else the host's, whose copies serve a server that needs nothing else.
// It is "" when neither can.
func pythonProgram(ctx context.Context, dir, host string) string {
	for _, program := range slices.Compact([]string{pythonEnvironment(dir, host), host}) {
		if exec.CommandContext(ctx, program, "-c", "import mcp.server, pydantic").Run() == nil {
			return program
		}
	}
	return ""
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
