package installation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/holon-sh/cli/pkg/typegen"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/internal/kiln"
)

var contractRunners = map[string]struct{ program, runner string }{
	"typescript": {"node", "runner.mjs"},
	"python":     {"python", "extract.py"},
}

var finalStage = regexp.MustCompile(`(?m)^FROM \S+ AS (\S+)$`)

// extractTypes is typegen's contract-runner hook. It builds the app's image with Kiln, without the app's
// build step, which may need the clients being prepared, and runs the runner there without network
// access, so the server's imports resolve against the dependencies its image installs.
func (o buildOptions) extractTypes(ctx context.Context, language, tooling string, args []string, stdout io.Writer) error {
	runner := contractRunners[language]
	env, err := commandenv.Environ(o.Env)
	if err != nil {
		return err
	}
	logs := o.output.logs(o.Stderr)
	command := slices.Concat([]string{runner.program, "/holon-tooling/" + language + "/" + runner.runner, args[0], "/app"}, args[2:])
	if err := extractInImage(ctx, args[1], tooling, command, stdout, env, logs); err != nil {
		if !errors.As(err, new(*typegen.RunnerError)) {
			logs.failed("MCP types")
		}
		return fmt.Errorf("prepare MCP types: %w", err)
	}
	return nil
}

// mentionsMCP reports whether any JavaScript or TypeScript source under root imports an MCP SDK or holon-sh.
func mentionsMCP(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if entry.IsDir() {
			if typegen.GeneratedSource(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if slices.Contains([]string{".ts", ".mts", ".cts", ".js", ".mjs", ".cjs", ".tsx", ".jsx"}, filepath.Ext(path)) {
			data, err := os.ReadFile(path)
			found = err == nil && (bytes.Contains(data, []byte("@modelcontextprotocol/")) || bytes.Contains(data, []byte(nodeRuntime)))
		}
		return nil
	})
	return found
}

func extractInImage(ctx context.Context, source, tooling string, command []string, stdout io.Writer, env []string, logs io.Writer) error {
	config, err := railpackConfig(filepath.Join(source, "railpack.json"))
	if err != nil {
		return err
	}
	objectField(objectField(config, "steps"), "build")["commands"] = []any{}
	if err := os.Mkdir(filepath.Join(source, ".holon-types"), 0o755); err != nil {
		return fmt.Errorf(".holon-types is reserved: %w", err)
	}
	if err := writeTypeJSON(filepath.Join(source, ".holon-types", "railpack.json"), config); err != nil {
		return err
	}
	plan, err := kiln.Plan(ctx, kiln.Request{
		Source: source, ConfigFilePath: ".holon-types/railpack.json", Platforms: []string{"linux/" + runtime.GOARCH},
	})
	if err != nil {
		return err
	}
	stages := finalStage.FindAllStringSubmatch(plan.Dockerfile, -1)
	words := make([]string, len(command))
	for i, word := range command {
		words[i] = "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
	}
	recipe := plan.Dockerfile + fmt.Sprintf("FROM %s AS contract\n"+
		"RUN --network=none --mount=type=bind,from=tooling,target=/holon-tooling,readonly mkdir /tmp/contract && "+
		"{ %s >/tmp/contract/stdout 2>/tmp/contract/stderr; echo $? >/tmp/contract/status; }\n"+
		"FROM scratch\nCOPY --from=contract /tmp/contract/ /\n", stages[len(stages)-1][1], strings.Join(words, " "))
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
	args := []string{
		"buildx", "build", "--builder", builder, "--provenance=false", "-f", dockerfile,
		"--build-context", "tooling=" + tooling, "--output", "type=local,dest=" + exported, source,
	}
	if err := commandenv.RunStreaming(ctx, "docker", args, nil, env, logs, logs); err != nil {
		return err
	}
	status, err := os.ReadFile(filepath.Join(exported, "status"))
	if err != nil {
		return err
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(status)))
	if err != nil {
		return err
	}
	if code != 0 {
		stderr, err := os.ReadFile(filepath.Join(exported, "stderr"))
		if err != nil {
			return err
		}
		return &typegen.RunnerError{Status: code, Stderr: string(stderr)}
	}
	result, err := os.ReadFile(filepath.Join(exported, "stdout"))
	if err != nil {
		return err
	}
	_, err = stdout.Write(result)
	return err
}
