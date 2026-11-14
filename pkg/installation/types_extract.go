package installation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/holon-sh/cli/pkg/typegen"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/internal/kiln"
)

// contractRunners are each language's interpreter, contract runner and checker, in its tooling directory.
var contractRunners = map[string]struct{ program, runner, checker string }{
	"typescript": {"node", "runner.mjs", "check.mjs"},
	"python":     {"python", "extract.py", "runner.py"},
}

// extractTypes is typegen's contract-runner hook. It runs the runner in the app's extraction image without
// network access, so the server's imports resolve against the dependencies its image installs.
func (o buildOptions) extractTypes(ctx context.Context, language, toolingDir string, args []string, stdout io.Writer) error {
	runner := contractRunners[language]
	command := slices.Concat([]string{runner.program, "/holon-tooling/" + language + "/" + runner.runner, args[0], "/app"}, args[2:])
	return o.runInImage(ctx, args[1], toolingDir, "", false, command, stdout)
}

// checkTypes type-checks each path app that serves a contract against its generated clients, in the image
// its contract was extracted in, so that the checker sees the dependencies installed there.
func (o buildOptions) checkTypes(ctx context.Context, apps []*manifestApp) error {
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(3)
	for _, app := range apps {
		p := app.resolved.types
		if entry, language, ok := p.prepared.CheckEntry(p.name); ok {
			group.Go(func() error { return o.checkInImage(ctx, p, entry, language) })
		}
	}
	return group.Wait()
}

func (o buildOptions) checkInImage(ctx context.Context, p *preparedTypes, entry, language string) error {
	scratch, err := os.MkdirTemp("", "holon-types-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	generated, checkers := filepath.Join(scratch, "generated"), filepath.Join(scratch, "tooling")
	if err := errors.Join(os.Mkdir(generated, 0o700), tooling.Unpack(checkers), p.prepared.WriteClients(p.name, generated)); err != nil {
		return err
	}
	runner := contractRunners[language]
	command := []string{runner.program, "/holon-tooling/" + language + "/" + runner.checker, "check", entry}
	var output bytes.Buffer
	err = o.runInImage(ctx, p.root, checkers, generated, language == "python", command, &output)
	if failed := (*typegen.RunnerError)(nil); errors.As(err, &failed) {
		return fmt.Errorf("%s does not type-check against its typed contracts:\n%s", p.name, strings.TrimSpace(output.String()+failed.Stderr))
	}
	return err
}

func (o buildOptions) runInImage(ctx context.Context, source, toolingDir, generated string, checker bool, command []string, stdout io.Writer) error {
	env, err := commandenv.Environ(o.Env)
	if err != nil {
		return err
	}
	run := kiln.Run{Command: command, Mounts: map[string]string{"tooling": "/holon-tooling"}}
	args := []string{"--build-context", "tooling=" + toolingDir}
	if generated != "" {
		run.Overlay = "generated"
		args = append(args, "--build-context", "generated="+generated)
	}
	if checker {
		run.Packages = []string{"mypy==2.4.0"}
	}
	recipe, err := kiln.Image(ctx, kiln.Request{Source: source, Run: &run})
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "holon-types-run-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	dockerfile, exported := filepath.Join(work, "Dockerfile"), filepath.Join(work, "output")
	if err := os.WriteFile(dockerfile, []byte(recipe), 0o600); err != nil {
		return err
	}
	builder, err := sourceBuilder(ctx, env)
	if err != nil {
		return err
	}
	args = slices.Concat([]string{"buildx", "build", "--builder", builder, "--provenance=false", "-f", dockerfile,
		"--output", "type=local,dest=" + exported}, args, []string{source})
	logs := o.output.logs(o.Stderr)
	if err := commandenv.RunStreaming(ctx, "", "docker", args, nil, env, logs, logs); err != nil {
		return logs.explain(err)
	}
	read := func(name string) ([]byte, error) {
		path := filepath.Join(exported, name)
		if err := regularNoSymlink(path, "MCP types "+name); err != nil {
			return nil, err
		}
		return os.ReadFile(path)
	}
	result, err := read("stdout")
	if err != nil {
		return err
	}
	if _, err := stdout.Write(result); err != nil {
		return err
	}
	status, err := read("status")
	if err != nil {
		return err
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(status)))
	if err != nil || code == 0 {
		return err
	}
	stderr, err := read("stderr")
	if err != nil {
		return err
	}
	return &typegen.RunnerError{Status: code, Stderr: string(stderr)}
}
