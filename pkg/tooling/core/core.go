// Package core owns extraction, source integrity and checking for both language workers.
// It does not embed assets: Go callers use it directly; bundled runners use the same
// library through the small holon-tooling executable.
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
)

type App struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Entry string `json:"entry"`
}

type Result struct {
	Tools      []mcpcontract.Tool     `json:"tools"`
	Resources  []mcpcontract.Resource `json:"resources"`
	Diagnostic string                 `json:"diagnostic,omitempty"`
	Sources    map[string]string      `json:"sources,omitempty"`
}

// Validate also applies to results supplied by an external extraction hook.
func (r Result) Validate(ctx context.Context) error {
	if r.Diagnostic != "" {
		return fmt.Errorf("%s", r.Diagnostic)
	}
	return mcptype.ValidateContract(ctx, mcpcontract.Contract{Tools: r.Tools, Resources: r.Resources})
}

// Runner uses the supplied interpreter and unpacked language runner. Env follows
// exec.Cmd's convention: nil inherits the host environment. Application code is
// never executed during extraction.
type Runner struct {
	Language, Program, Path string
	Env                     []string
	Stdout, Stderr          io.Writer
}

func (r Runner) run(ctx context.Context, dir string, input any, output io.Writer, args ...string) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, r.Program, args...)
	command.Dir, command.Env = dir, r.Env
	command.Stdin, command.Stdout, command.Stderr = bytes.NewReader(data), output, r.Stderr
	if err = command.Run(); ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (r Runner) Extract(ctx context.Context, apps []App) (map[string]Result, error) {
	results := make(map[string]Result, len(apps))
	for _, app := range apps {
		var output, diagnostics bytes.Buffer
		worker := r
		worker.Stderr = &diagnostics
		if err := worker.run(ctx, "", nil, &output, r.Path, "analyze", app.Path, app.Entry); err != nil {
			return nil, fmt.Errorf("%w: %s", err, &diagnostics)
		}
		var parsed struct {
			Result
			Ignored []string `json:"ignored"`
		}
		if err := json.Unmarshal(output.Bytes(), &parsed); err != nil {
			return nil, err
		}
		result := parsed.Result
		err := result.Validate(ctx)
		if err == nil && r.Language == "typescript" {
			// Keep Ajv's JavaScript regex and draft semantics. Go owns which schemas
			// are checked; the worker only calls the existing schema compiler.
			schemas := []json.RawMessage{}
			for _, tool := range result.Tools {
				schemas = append(schemas, tool.Input)
				if tool.Output != nil {
					schemas = append(schemas, tool.Output)
				}
			}
			for _, resource := range result.Resources {
				schemas = append(schemas, resource.Output)
			}
			err = worker.run(ctx, "", schemas, &output, r.Path, "schemas")
			if err != nil {
				err = fmt.Errorf("unsupported JSON Schema: %w: %s", err, &diagnostics)
			}
		}
		if err == nil {
			result.Sources, err = fingerprint(app.Path, r.Language, result.Sources, parsed.Ignored)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			result = Result{Diagnostic: err.Error()}
		}
		if result.Resources == nil {
			result.Resources = []mcpcontract.Resource{}
		}
		results[app.Name] = result
	}
	return results, nil
}

type CheckConfig struct {
	Root    string            `json:"root"`
	Entry   string            `json:"entry"`
	Sources map[string]string `json:"sources"`
	Check   bool              `json:"check"`
}

// Check verifies the extraction manifest before invoking the application's checker.
// verifyOnly is the post-build guard used by existing image builders.
func (r Runner) Check(ctx context.Context, config CheckConfig, verifyOnly bool) error {
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return err
	}
	if err = Verify(root, config.Sources); err != nil || verifyOnly {
		return err
	}
	config.Root = root
	if r.Language == "python" {
		if !config.Check {
			return nil
		}
		entry := config.Entry
		if entry == "" {
			entry = "."
		}
		return r.run(ctx, root, nil, r.Stdout, "-m", "mypy", "--no-incremental", "--check-untyped-defs", "--explicit-package-bases", "--exclude", `/(\.venv|venv|node_modules)/`, entry)
	}
	return r.run(ctx, root, config, r.Stdout, r.Path, "compile")
}

// Run preserves the file-based extract/check/verify protocol used by image builds.
func (r Runner) Run(ctx context.Context, mode, configPath, output string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	switch mode {
	case "extract":
		var config struct {
			Apps []App `json:"apps"`
		}
		if err = json.Unmarshal(data, &config); err != nil {
			return err
		}
		results, err := r.Extract(ctx, config.Apps)
		if err != nil {
			return err
		}
		data, err = json.Marshal(results)
		if err != nil {
			return err
		}
		return os.WriteFile(output, data, 0o600)
	case "check", "verify":
		var config CheckConfig
		if err = json.Unmarshal(data, &config); err != nil {
			return err
		}
		return r.Check(ctx, config, mode == "verify")
	default:
		return fmt.Errorf("unknown internal type preparation operation: %s", mode)
	}
}
