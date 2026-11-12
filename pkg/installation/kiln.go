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
	"regexp"
	"slices"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"

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
	dockerfile string
	upload     *imageUpload
	httpClient *http.Client
	builders   *builderConnections
	output     *progressOutput
}

func buildSource(ctx context.Context, app manifestApp, upload *imageUpload, options buildOptions) (string, string, error) {
	if !protocol.ValidBuildArchitectures(options.architectures) {
		return "", "", errors.New("invalid build architectures")
	}
	p := app.resolved.types
	request, err := options.planSource(ctx, app, p)
	if err != nil {
		return "", "", err
	}
	request.upload = upload
	kept := options.kept[app.Name]
	image := kept.image
	if kept.inputs != p.inputs || image == "" {
		options.output.status(app.Name, "building", progressWorking)
		build := options.builder
		if build == nil {
			build = publishSource
		}
		if image, err = build(ctx, request, options.Stderr); err != nil {
			return "", "", fmt.Errorf("build %s: %w", app.Name, err)
		}
	}
	if !request.Development {
		return image, "", nil
	}
	overlay, err := request.pushOverlay(ctx, kept.overlay)
	return image, overlay, err
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
		Request: kiln.Request{Source: p.root, Start: start, Development: o.development && start != nil},
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
	if err != nil {
		return buildRequest{}, err
	}
	request.dockerfile = plan.Dockerfile
	if request.Development {
		p.inputs, err = imageInputs(ctx, plan.Dockerfile, p.root)
	}
	return request, err
}

var contextCopy = regexp.MustCompile(`(?m)^COPY ((?:--exclude=\S+ )*)(\[.*\])$`)

func imageInputs(ctx context.Context, dockerfile, root string) (string, error) {
	source, err := fsutil.NewFS(root)
	if err != nil {
		return "", err
	}
	digester := digest.SHA256.Digester()
	_, _ = io.WriteString(digester.Hash(), dockerfile)
	for _, match := range contextCopy.FindAllStringSubmatch(dockerfile, -1) {
		var paths []string
		if err := json.Unmarshal([]byte(match[2]), &paths); err != nil {
			return "", err
		}
		filter := &fsutil.FilterOpt{IncludePatterns: paths[:len(paths)-1], Map: func(path string, s *fstypes.Stat) fsutil.MapResult {
			if s.IsDir() {
				return fsutil.MapResultExclude
			}
			return stableStat(path, s)
		}}
		if slices.Contains(filter.IncludePatterns, ".") {
			filter.IncludePatterns = nil
		}
		for exclusion := range strings.FieldsSeq(match[1]) {
			filter.ExcludePatterns = append(filter.ExcludePatterns, strings.TrimPrefix(exclusion, "--exclude="))
		}
		copied, err := fsutil.NewFilterFS(source, filter)
		if err != nil {
			return "", err
		}
		if err := fsutil.WriteTar(ctx, copied, digester.Hash()); err != nil {
			return "", err
		}
	}
	return digester.Digest().Encoded(), nil
}

func stageSnapshot(app manifestApp, p *preparedTypes) ([]string, error) {
	if err := stageTypes(p); err != nil {
		return nil, err
	}
	if _, err := hookartifact.Discover(os.DirFS(p.root)); err != nil {
		return nil, err
	}
	if app.resolved.stdio {
		return nil, nil
	}
	entry, err := sourceEntry(p.root)
	if err != nil || !slices.Contains([]string{".py", ".ts", ".js"}, filepath.Ext(entry)) {
		return nil, err
	}
	hostDir := filepath.Join(p.root, ".holon")
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
