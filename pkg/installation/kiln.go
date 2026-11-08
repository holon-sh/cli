package installation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/internal/kiln"
	"github.com/holon-sh/protocol"
	"github.com/holon-sh/protocol/hookartifact"
	"github.com/holon-sh/runtime/host/javascript"
	"github.com/holon-sh/runtime/host/python"
)

type buildApplication func(context.Context, buildRequest, io.Writer) (string, error)

type buildRequest struct {
	kiln.Request

	Env        []string
	app        manifestApp
	keys       map[string]string // the imageKeys published with the image
	upload     *imageUpload
	httpClient *http.Client
	builders   *builderConnections
	output     *progressOutput
}

func buildSource(ctx context.Context, app manifestApp, upload *imageUpload, options buildOptions, keys map[string]string) (string, error) {
	if !protocol.ValidBuildArchitectures(options.architectures) {
		return "", errors.New("invalid build architectures")
	}
	p := app.resolved.types
	config, start, err := stageSnapshot(app, p)
	if err != nil {
		return "", err
	}
	env, err := commandenv.Environ(options.Env)
	if err != nil {
		return "", err
	}
	request := buildRequest{
		Request: kiln.Request{ConfigFilePath: config, Source: p.root, Start: start}, Env: env, app: app, keys: keys,
		upload: upload, httpClient: options.HTTPClient, builders: options.builders, output: options.output,
	}
	for _, arch := range buildArchitectures(options.architectures) {
		request.Platforms = append(request.Platforms, "linux/"+arch)
	}
	build := options.builder
	if build == nil {
		build = publishSource
	}
	image, err := build(ctx, request, options.Stderr)
	if err != nil {
		return "", fmt.Errorf("build %s: %w", app.Name, err)
	}
	return image, nil
}

// buildArchitectures are the architectures images are built for: the installation's, or both by default.
func buildArchitectures(configured []string) []string {
	if len(configured) == 0 {
		return []string{"amd64", "arm64"}
	}
	return configured
}

// stageSnapshot fills p's snapshot as an image build or a source overlay reads it: the generated clients,
// then the SDK launcher for a hosted entry. It returns the build configuration and start command, either
// of which may be empty.
func stageSnapshot(app manifestApp, p *preparedTypes) (string, []string, error) {
	config, err := stageTypes(p)
	if err != nil {
		return "", nil, err
	}
	if _, err := hookartifact.Discover(os.DirFS(p.root)); err != nil {
		return "", nil, err
	}
	var entry string
	if !app.resolved.stdio {
		if entry, err = sourceEntry(p.root); err != nil {
			return "", nil, err
		}
	}
	start, err := stageLauncher(p.root, entry)
	return config, start, err
}

func hostedEntry(entry string) bool {
	return slices.Contains([]string{".py", ".ts", ".js"}, filepath.Ext(entry))
}

// stageLauncher writes the SDK launcher for a hosted server entry into snapshot and returns its start
// command, or nothing for an entry that Holon does not host.
func stageLauncher(snapshot, entry string) ([]string, error) {
	if !hostedEntry(entry) {
		return nil, nil
	}
	hostDir := filepath.Join(snapshot, ".holon")
	if err := os.Mkdir(hostDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare SDK launcher (.holon is reserved): %w", err)
	}
	adapter, launcher, contents := "adapter.mjs", "main.mjs", javascript.Bundle
	bootstrap := "import { start } from './adapter.mjs';\nawait start(() => import('../" + entry + "'));\n"
	start := []string{"node", "./.holon/main.mjs"}
	if filepath.Ext(entry) == ".py" {
		adapter, launcher, contents = "holon_host.py", "main.py", python.Adapter
		module := strings.TrimSuffix(strings.ReplaceAll(entry, "/", "."), ".py")
		bootstrap = fmt.Sprintf("import asyncio\nfrom holon_host import start\nasyncio.run(start(%q))\n", module)
		start = []string{"python", "./.holon/main.py"}
	}
	if err := mcpcontract.WriteStable(filepath.Join(hostDir, adapter), []byte(contents), 0o644); err != nil {
		return nil, err
	}
	if err := mcpcontract.WriteStable(filepath.Join(hostDir, launcher), []byte(bootstrap), 0o644); err != nil {
		return nil, err
	}
	return start, nil
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
