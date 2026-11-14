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

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/internal/kiln"
	"github.com/holon-sh/protocol"
	"github.com/holon-sh/protocol/hookartifact"
)

type buildApplication func(context.Context, buildRequest, io.Writer) (string, error)

type buildRequest struct {
	kiln.Request

	Env        []string
	app        manifestApp
	dockerfile string
	upload     *imageUpload
	httpClient *http.Client
	builders   *builderConnections
	output     *progressOutput
}

func buildSource(ctx context.Context, app manifestApp, upload *imageUpload, options buildOptions) (string, error) {
	if !protocol.ValidBuildArchitectures(options.architectures) {
		return "", errors.New("invalid build architectures")
	}
	request, err := options.planSource(ctx, app, app.resolved.types)
	if err != nil {
		return "", err
	}
	request.upload = upload
	options.output.status(app.Name, "building", progressWorking)
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

func (o buildOptions) planSource(ctx context.Context, app manifestApp, p *preparedTypes) (buildRequest, error) {
	start, err := stageSnapshot(app, p)
	if err != nil {
		return buildRequest{}, err
	}
	env, err := commandenv.Environ(o.Env)
	if err != nil {
		return buildRequest{}, err
	}
	request := buildRequest{
		Request: kiln.Request{Source: p.root, Start: start},
		Env:     env, app: app, httpClient: o.HTTPClient, builders: o.builders, output: o.output,
	}
	architectures := o.architectures
	if len(architectures) == 0 {
		architectures = []string{"amd64", "arm64"}
	}
	for _, arch := range architectures {
		request.Platforms = append(request.Platforms, "linux/"+arch)
	}
	planned := o.output.measure(app.Name, "plan")
	plan, err := kiln.Plan(ctx, request.Request)
	planned()
	request.dockerfile = plan.Dockerfile
	return request, err
}

func stageSnapshot(app manifestApp, p *preparedTypes) ([]string, error) {
	if err := stageTypes(p); err != nil {
		return nil, err
	}
	entry, err := hostedEntry(app, p.root)
	if err != nil || entry == "" {
		return nil, err
	}
	return serveCommand(entry), nil
}

// serveCommand starts an app from its entry through the SDK, which serves the app itself.
func serveCommand(entry string) []string {
	if filepath.Ext(entry) == ".py" {
		return []string{"python", "-m", "holon.serve", entry}
	}
	return []string{"node_modules/.bin/holon-serve", entry}
}

func hostedEntry(app manifestApp, root string) (string, error) {
	if _, err := hookartifact.Discover(os.DirFS(root)); err != nil {
		return "", err
	}
	if app.resolved.stdio {
		return "", nil
	}
	entry, err := sourceEntry(root)
	if err != nil || !slices.Contains([]string{".py", ".ts", ".js"}, filepath.Ext(entry)) {
		return "", err
	}
	return entry, nil
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
