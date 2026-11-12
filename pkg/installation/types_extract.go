package installation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/tailscale/hujson"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/internal/kiln"
)

// contractRunners are each language's interpreter, contract runner and checker, in its tooling directory.
var contractRunners = map[string]struct{ program, runner, checker string }{
	"typescript": {"node", "runner.mjs", "check.mjs"},
	"python":     {"python", "extract.py", "runner.py"},
}

var finalStage = regexp.MustCompile(`(?m)^FROM (\S+) AS (\S+)$`)

// checkerInstall installs the Python type checker on the runtime's base image alone, so its layer survives
// source and dependency changes. A check imports it from PYTHONPATH with the app's own interpreter.
const checkerInstall = "python -m pip install --disable-pip-version-check --no-cache-dir --root-user-action=ignore --target /opt/holon-check mypy==2.4.0"

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
	generated, err := os.MkdirTemp("", "holon-types-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(generated)
	if err := p.prepared.WriteClients(p.name, generated); err != nil {
		return err
	}
	runner := contractRunners[language]
	command := []string{runner.program, "/holon-tooling/" + language + "/" + runner.checker, "check", entry}
	var output bytes.Buffer
	err = o.runInImage(ctx, p.root, p.prepared.Tooling(), generated, language == "python", command, &output)
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
	config, err := railpackConfig(filepath.Join(source, "railpack.json"))
	if err != nil {
		return err
	}
	objectField(objectField(config, "steps"), "build")["commands"] = []any{}
	directory := filepath.Join(source, ".holon-types")
	if err := os.Mkdir(directory, 0o755); err != nil {
		return fmt.Errorf(".holon-types is reserved: %w", err)
	}
	defer os.RemoveAll(directory)
	data, _ := json.MarshalIndent(config, "", "  ")
	if err := mcpcontract.WriteStable(filepath.Join(directory, "railpack.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	plan, err := kiln.Plan(ctx, kiln.Request{
		Source: source, ConfigFilePath: ".holon-types/railpack.json", Platforms: []string{"linux/" + runtime.GOARCH},
	})
	if err != nil {
		return err
	}
	stages := finalStage.FindAllStringSubmatch(plan.Dockerfile, -1)
	base, final := stages[len(stages)-1][1], stages[len(stages)-1][2]
	args := []string{"--build-context", "tooling=" + toolingDir}
	recipe, mounts := plan.Dockerfile, "--mount=type=bind,from=tooling,target=/holon-tooling,readonly"
	if checker {
		command = slices.Concat([]string{"env", "PYTHONPATH=/opt/holon-check", "MYPY_CACHE_DIR=/dev/null"}, command)
		recipe += "FROM " + base + " AS checker\nRUN " + checkerInstall + "\n"
		mounts += " --mount=type=bind,from=checker,source=/opt/holon-check,target=/opt/holon-check,readonly"
	}
	recipe += "FROM " + final + " AS contract\n"
	if generated != "" {
		args = append(args, "--build-context", "generated="+generated)
		recipe += "COPY --from=generated . /app/\n"
	}
	words := make([]string, len(command))
	for i, word := range command {
		words[i] = "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
	}
	recipe += fmt.Sprintf("RUN --network=none %s mkdir /tmp/contract && "+
		"{ %s >/tmp/contract/stdout 2>/tmp/contract/stderr; echo $? >/tmp/contract/status; }\n"+
		"FROM scratch\nCOPY --from=contract /tmp/contract/ /\n", mounts, strings.Join(words, " "))
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

func railpackConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	config := map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		if data, err = hujson.Standardize(data); err != nil {
			return nil, fmt.Errorf("railpack.json: %w", err)
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("railpack.json: %w", err)
		}
	}
	return config, nil
}

func objectField(parent map[string]any, name string) map[string]any {
	value, ok := parent[name].(map[string]any)
	if !ok {
		value = map[string]any{}
		parent[name] = value
	}
	return value
}
