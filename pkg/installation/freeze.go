package installation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/recipe"
	"github.com/holon-sh/cli/pkg/sourcebuild"
	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/holon-sh/protocol"
	"github.com/holon-sh/protocol/runtimecontract"
)

const buildSessionSeconds = 60 * 60

var contractRunners = map[string]struct {
	program string
	check   []string
}{
	"typescript": {"node", []string{"node"}},
	"python":     {".venv/bin/python", []string{"uv", "run", "--no-sync", "--with", "mypy==2.4.0", "python"}},
}

type sourceBuild struct {
	app    manifestApp
	start  []string
	recipe recipe.Recipe
}

type buildSession struct {
	manager *Client
	output  *progressOutput
	session protocol.SessionResponse
	sources map[string]sourceBuild
}

func (b *buildSession) start(ctx context.Context, apps []*manifestApp) error {
	if len(apps) == 0 {
		return nil
	}
	if b.manager.info.ImageRepositoryPrefix != "" {
		return fmt.Errorf("%s: holon deploy builds source apps in the installation's registry, which this installation turns off; ship it as an image: app", apps[0].Name)
	}
	b.sources = map[string]sourceBuild{}
	var request protocol.AppsRequest
	for _, app := range apps {
		root := app.resolved.types.root
		start, err := sourceAppStart(*app, root)
		if err != nil {
			return err
		}
		steps, err := recipe.Read(os.DirFS(root))
		if err != nil {
			return fmt.Errorf("%s: %w; ship it as an image: app", app.Name, err)
		}
		b.sources[app.Path] = sourceBuild{app: *app, start: start, recipe: steps}
		request.Apps = append(request.Apps, protocol.App{
			Name: app.Name, Source: true, Access: &protocol.AppAccess{Server: &protocol.AccessRule{Callers: []string{}}},
		})
	}
	request.TTLSeconds = new(int64(buildSessionSeconds))
	created := b.output.measure("build session", "create")
	err := b.manager.Call(ctx, protocol.DevPlane, protocol.ToolCreateSession, request, &b.session)
	created()
	if err != nil {
		return err
	}
	b.session, err = waitSessionReady(ctx, b.manager, b.session, b.output)
	return err
}

func (b *buildSession) close(ctx context.Context) error {
	if b.session.Name == "" {
		return nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	return b.manager.DeleteSession(cleanup, b.session.Name, b.session.UID, true)
}

func (b *buildSession) extract(ctx context.Context, language, toolingDir string, args []string, stdout io.Writer) error {
	source := b.sources[args[1]]
	defer b.output.measure(source.app.Name, "contract")()
	directory, err := os.MkdirTemp("", "holon-contract-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	root := filepath.Join(directory, "source")
	if err := errors.Join(
		os.CopyFS(root, os.DirFS(source.app.resolved.types.root)), os.CopyFS(filepath.Join(root, ".holon", "tooling"), os.DirFS(filepath.Join(toolingDir, language))),
	); err != nil {
		return err
	}
	command := slices.Concat([]string{contractRunners[language].program}, typegen.RunnerCommand(language, path.Join(".holon", "tooling")), []string{args[0], "."}, args[2:])
	plan := source.recipe.Deploy.Plan()
	plan.Prepare = nil
	reply, err := b.build(ctx, source.app.Name, root, plan, runtimecontract.Build{Run: command})
	if err != nil {
		return err
	}
	if reply.Ran.ExitCode != 0 {
		return &typegen.RunnerError{Status: reply.Ran.ExitCode, Stderr: reply.Ran.Stderr}
	}
	_, err = io.WriteString(stdout, reply.Ran.Stdout)
	return err
}

func (b *buildSession) build(
	ctx context.Context, app, root string, plan runtimecontract.Plan, build runtimecontract.Build,
) (runtimecontract.BuildReply, error) {
	_, file, err := packOverlay(ctx, root, plan, &build)
	if err != nil {
		return runtimecontract.BuildReply{}, err
	}
	defer func() { _ = os.Remove(file) }()
	var reply runtimecontract.BuildReply
	err = b.manager.postOverlay(ctx, b.session.Name, app, "build", file, &reply)
	if err == nil {
		return reply, nil
	}
	if failure, ok := errors.AsType[*httpError](err); ok && failure.Message != "" {
		return reply, fmt.Errorf("build %s: %s\n%s", app, failure.Message, strings.TrimSpace(failure.Output))
	}
	return reply, fmt.Errorf("build %s: %w", app, err)
}

func buildSource(ctx context.Context, app manifestApp, upload *imageUpload, options buildOptions) (ocispec.Descriptor, error) {
	source := options.build.sources[app.Path]
	p := app.resolved.types
	if err := p.prepared.WriteClients(p.name, p.root); err != nil {
		return ocispec.Descriptor{}, err
	}
	build := runtimecontract.Build{Freeze: &source.recipe.Deploy.Freeze}
	if checked, language, ok := p.prepared.CheckEntry(p.name); ok {
		checker := typegen.Checker(language)
		script, err := tooling.File(language, checker)
		if err == nil {
			err = writeSourceFile(p.root, path.Join(".holon", checker), script)
		}
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		build.Run = slices.Concat(contractRunners[language].check, []string{path.Join(".holon", checker), "check", checked})
	}
	built := options.output.measure(app.Name, "build")
	reply, err := options.build.build(ctx, app.Name, p.root, source.recipe.Deploy.Plan(), build)
	built()
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if reply.Ran != nil && reply.Ran.ExitCode != 0 {
		return ocispec.Descriptor{}, fmt.Errorf("%s does not type-check against its typed contracts:\n%s", app.Name, strings.TrimSpace(reply.Ran.Stdout+reply.Ran.Stderr))
	}
	frozen, err := digest.Parse(reply.Image)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("build %s: the build froze no image: %w", app.Name, err)
	}
	defer options.output.measure(app.Name, "assemble")()
	var image ocispec.Descriptor
	err = upload.repository(ctx, app.Name, func(repository *remote.Repository, grant protocol.UploadGrant) error {
		development, err := remote.NewRepository(grant.Registry + "/" + protocol.BaseRepository)
		if err != nil {
			return err
		}
		development.Client = repository.Client
		ctx := auth.AppendRepositoryScope(ctx, repository.Reference, auth.ActionPull, auth.ActionPush)
		ctx = auth.AppendRepositoryScope(ctx, development.Reference, auth.ActionPull)
		manifest, err := sourcebuild.Assemble(ctx, repository, development, frozen, source.start, app.resolved.descriptor.OCILabels())
		if err != nil {
			return err
		}
		index := ocispec.Index{
			Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex,
			Manifests: []ocispec.Descriptor{manifest},
		}
		if p.metadata != nil {
			metadata, ownDigest, err := pushTypeMetadata(ctx, repository, p.metadata, p.documents)
			if err != nil {
				return err
			}
			index.Manifests = append(index.Manifests, metadata)
			index.Annotations = map[string]string{mcpcontract.DigestAnnotation: ownDigest}
		}
		data, _ := json.Marshal(index)
		image, err = oras.PushBytes(ctx, repository, index.MediaType, data)
		return err
	})
	return image, err
}
