package installation

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/internal/kiln"
	"github.com/holon-sh/protocol"
	"github.com/holon-sh/protocol/hookartifact"
	"github.com/holon-sh/runtime/host/javascript"
	"github.com/holon-sh/runtime/host/python"
)

type buildApplication func(context.Context, buildRequest, io.Writer) (buildResult, error)

type buildRequest struct {
	kiln.Request

	Env        []string
	app        manifestApp
	upload     *imageUpload
	httpClient *http.Client
	builders   *builderConnections
	output     *progressOutput
}

type buildResult struct {
	Image      string
	Resolution json.RawMessage
}

func buildSource(ctx context.Context, app manifestApp, upload *imageUpload, options buildOptions) (string, error) {
	if !protocol.ValidBuildArchitectures(options.architectures) {
		return "", errors.New("invalid build architectures")
	}
	p := app.resolved.types
	snapshot := p.root
	staged := options.output.measure(app.Name, "stage types")
	config, err := stageTypes(p, options.check)
	staged()
	if err != nil {
		return "", err
	}
	if _, err := hookartifact.Discover(os.DirFS(snapshot)); err != nil {
		return "", err
	}
	var entry string
	if !app.resolved.stdio {
		entry, err = sourceEntry(snapshot)
		if err != nil {
			return "", err
		}
	}
	var start []string
	if slices.Contains([]string{".py", ".ts", ".js"}, filepath.Ext(entry)) {
		hostDir := filepath.Join(snapshot, ".holon")
		if err := os.Mkdir(hostDir, 0o755); err != nil {
			return "", fmt.Errorf("prepare SDK launcher (.holon is reserved): %w", err)
		}
		adapter, launcher, contents := "adapter.mjs", "main.mjs", javascript.Bundle
		bootstrap := "import { start } from './adapter.mjs';\nawait start(() => import('../" + entry + "'));\n"
		start = []string{"node", "./.holon/main.mjs"}
		if filepath.Ext(entry) == ".py" {
			adapter, launcher, contents = "holon_host.py", "main.py", python.Adapter
			module := strings.TrimSuffix(strings.ReplaceAll(entry, "/", "."), ".py")
			bootstrap = fmt.Sprintf("import asyncio\nfrom holon_host import start\nasyncio.run(start(%q))\n", module)
			start = []string{"python", "./.holon/main.py"}
		}
		if err := mcpcontract.WriteStable(filepath.Join(hostDir, adapter), []byte(contents), 0o644); err != nil {
			return "", err
		}
		if err := mcpcontract.WriteStable(filepath.Join(hostDir, launcher), []byte(bootstrap), 0o644); err != nil {
			return "", err
		}
	}

	architectures := options.architectures
	if len(architectures) == 0 {
		architectures = []string{"amd64", "arm64"}
	}
	env, err := commandenv.Environ(options.Env)
	if err != nil {
		return "", err
	}
	request := buildRequest{
		Request: kiln.Request{ConfigFilePath: config, Source: snapshot, Start: start}, Env: env, app: app, upload: upload,
		httpClient: options.HTTPClient, builders: options.builders, output: options.output,
	}
	for _, arch := range architectures {
		request.Platforms = append(request.Platforms, "linux/"+arch)
	}
	options.resolutions = cmp.Or(options.resolutions, new(sync.Map))
	previous, _ := options.resolutions.Load(app.Name)
	request.Resolution, _ = previous.(json.RawMessage)
	build := options.builder
	if build == nil {
		build = publishSource
	}
	result, err := build(ctx, request, options.Stderr)
	if err != nil {
		return "", fmt.Errorf("build %s: %w", app.Name, err)
	}
	options.resolutions.Store(app.Name, result.Resolution)
	return result.Image, nil
}

func sourceLabels(app manifestApp) map[string]string {
	if d := app.resolved.descriptor; d != nil {
		return map[string]string{
			"io.modelcontextprotocol.server.name": d.Name, ocispec.AnnotationTitle: cmp.Or(d.Title, d.Name),
			ocispec.AnnotationVersion: d.Version, ocispec.AnnotationDescription: d.Description,
			ocispec.AnnotationURL: d.WebsiteURL, ocispec.AnnotationSource: d.Repository.URL,
		}
	}
	return nil
}
