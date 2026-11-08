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
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/opencontainers/go-digest"
)

// extraction is the contract a runner prints: the server's catalog and the application files it
// loaded, with their SHA-256 digests.
type extraction struct {
	Tools      []mcpcontract.Tool     `json:"tools"`
	Resources  []mcpcontract.Resource `json:"resources"`
	ServerInfo json.RawMessage        `json:"serverInfo"`
	Sources    map[string]string      `json:"sources"`
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

// missing is the uninstalled package that a status 3 names, or nil.
func (e *RunnerError) missing() *missingPackage {
	if e.Status != 3 {
		return nil
	}
	lines := strings.Split(strings.TrimRight(e.Stderr, "\n"), "\n")
	return &missingPackage{name: lines[len(lines)-1], diagnostic: strings.Join(lines[:len(lines)-1], "\n")}
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
	"typescript": {[]string{"--experimental-vm-modules", "--no-warnings"}, "runner.mjs", []string{"check.mjs", "check"}, []string{"node"}, []string{"-e", "process.exit(Number(process.versions.node.split('.')[0]) < 22 ? 1 : 0)"}},
	"python":     {nil, "extract.py", []string{"runner.py", "check"}, []string{"python3", "python3.15", "python3.14", "python3.13", "python3.12", "python3.11"}, []string{"-I", "-c", "import sys; sys.exit(sys.version_info < (3, 11))"}},
}

// command runs step, one of the language's tooling commands, unpacked under tooling, with args.
func (l typeLanguage) command(ctx context.Context, program, tooling, language string, step []string, args ...string) *exec.Cmd {
	arguments := append([]string{filepath.Join(tooling, language, step[0])}, step[1:]...)
	return exec.CommandContext(ctx, program, append(arguments, args...)...)
}

// Tooling is the embedded tooling and the host interpreters that the calls of one command share:
// each language's tooling is unpacked when a call first needs it, and each interpreter is probed
// once. The zero Tooling is ready to use; Close removes what it unpacked.
type Tooling struct {
	mu       sync.Mutex
	root     string
	unpacked map[string]error
	probed   map[string]bool
}

// Close removes the unpacked tooling.
func (t *Tooling) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	root := t.root
	t.root, t.unpacked = "", nil
	if root == "" {
		return nil
	}
	return os.RemoveAll(root)
}

// dir returns a directory that holds only the language's tooling, under dir/<language>.
func (t *Tooling) dir(language string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.root == "" {
		root, err := os.MkdirTemp("", "holon-types-")
		if err != nil {
			return "", err
		}
		t.root, t.unpacked = root, map[string]error{}
	}
	dir := filepath.Join(t.root, language)
	if _, done := t.unpacked[language]; !done {
		t.unpacked[language] = tooling.Unpack(dir, language)
	}
	return dir, t.unpacked[language]
}

// supports reports whether program is an interpreter that runs the language's tooling.
func (t *Tooling) supports(ctx context.Context, language, program string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if supported, probed := t.probed[program]; probed {
		return supported
	}
	supported := exec.CommandContext(ctx, program, typeLanguages[language].probe...).Run() == nil
	if ctx.Err() == nil {
		if t.probed == nil {
			t.probed = map[string]bool{}
		}
		t.probed[program] = supported
	}
	return supported
}

// host returns the first of the language's host interpreters that runs its tooling.
func (t *Tooling) host(ctx context.Context, language string) (string, error) {
	l := typeLanguages[language]
	for _, program := range l.programs {
		if t.supports(ctx, language, program) {
			return program, nil
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", fmt.Errorf("install a supported interpreter (%s)", strings.Join(l.programs, ", "))
}

// tools returns the command's shared tooling, or tooling of the call's own that release removes.
func (o Options) tools() (t *Tooling, release func()) {
	if o.Tooling != nil {
		return o.Tooling, func() {}
	}
	t = &Tooling{}
	return t, func() { _ = t.Close() }
}

// ContractFile is a server's committed contract document, beside its server.json: what
// holon types generate --contract prints for it. It stands in for running the server when a
// package the server imports is not installed.
const ContractFile = "holon-contract.json"

// missingPackage is a runner's report that the server stopped on an uninstalled package.
type missingPackage struct{ name, diagnostic string }

func (m *missingPackage) Error() string { return m.name + " is not installed" }

// extractSources obtains each non-Go path source's contract by running its server with tools, or
// from its committed ContractFile when a package the server imports is not installed. With drift, a
// committed contract that differs from what the server answers is an error.
func extractSources(ctx context.Context, root string, tools *Tooling, sources []*preparedSource, o Options, drift bool) error {
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
			// A hook may run servers where their own directories are not visible, such as a container
			// without network access, so it gets a snapshot under root: a server that needs only its
			// schema libraries extracts with the tooling's copies, and others report the package they miss.
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
		group.Go(func() { results[i], failures[i] = runServer(ctx, tools, directories[s], s, o) })
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
			same, err := mcptype.SameContract(ctx, s.metadata.Schema, document.Schema)
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
	if s.metadata.Identity != (mcpcontract.Identity{}) && document.Identity != s.metadata.Identity {
		return nil, fmt.Errorf("%s describes %s@%s but server.json names %s@%s", path, document.Identity.Name, document.Identity.Version, s.metadata.Identity.Name, s.metadata.Identity.Version)
	}
	return document, nil
}

// runServer runs the source's server, in dir, through its language's contract runner and parses
// the contract it prints.
func runServer(ctx context.Context, tools *Tooling, dir string, s *preparedSource, o Options) (*extraction, error) {
	toolingDir, err := tools.dir(s.language)
	if err != nil {
		return nil, err
	}
	args := []string{"contract", dir, s.entry}
	var stdout bytes.Buffer
	if o.Run != nil {
		err = o.Run(ctx, s.language, toolingDir, args, &stdout)
	} else {
		err = runLocal(ctx, tools, toolingDir, s.language, args, &stdout, o.Env)
	}
	var failed *RunnerError
	switch {
	case errors.As(err, &failed) && failed.missing() != nil:
		return nil, failed.missing()
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

// runLocal runs a contract runner, unpacked under toolingDir, on the host. A Python server (args[1])
// runs in its own environment first, and with the host's interpreter, whose copies of mcp and
// pydantic serve a server that needs nothing else, when it has none or that lacks them.
func runLocal(ctx context.Context, tools *Tooling, toolingDir, language string, args []string, stdout io.Writer, env []string) error {
	if err := validateEnv(env); err != nil {
		return err
	}
	if language == "python" {
		if own := pythonEnvironment(args[1]); own != "" && tools.supports(ctx, language, own) {
			if err := runRunner(ctx, own, toolingDir, language, args, stdout, env); !lacksSchemaLibraries(err) {
				return err
			}
		}
	}
	program, err := tools.host(ctx, language)
	if err != nil {
		return err
	}
	err = runRunner(ctx, program, toolingDir, language, args, stdout, env)
	if language == "python" && lacksSchemaLibraries(err) {
		return &RunnerError{Status: 3, Stderr: "no Python environment here imports mcp and pydantic\nmcp\n"}
	}
	return err
}

// runRunner runs the language's contract runner with program, stopping the server after
// serverTimeout.
func runRunner(ctx context.Context, program, toolingDir, language string, args []string, stdout io.Writer, env []string) error {
	l := typeLanguages[language]
	limited, cancel := context.WithTimeout(ctx, serverTimeout)
	defer cancel()
	command := exec.CommandContext(limited, program, slices.Concat(l.flags, []string{filepath.Join(toolingDir, language, l.runner)}, args)...)
	command.Env = append(command.Environ(), env...)
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

// lacksSchemaLibraries reports a Python runner's stop on an uninstalled mcp or pydantic.
func lacksSchemaLibraries(err error) bool {
	var failed *RunnerError
	if !errors.As(err, &failed) {
		return false
	}
	missing := failed.missing()
	return missing != nil && (missing.name == "mcp" || missing.name == "pydantic")
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
