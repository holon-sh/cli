package installation

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/ociimage"
	"github.com/holon-sh/cli/pkg/recipe"
	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/holon-sh/protocol"
	"github.com/holon-sh/protocol/runtimecontract"
)

const buildSessionSeconds = 60 * 60

var contractRunners = map[string]struct {
	program, runner, checker string
	check                    []string
}{
	"typescript": {"node", "runner.mjs", "check.mjs", []string{"node"}},
	"python":     {".venv/bin/python", "extract.py", "runner.py", []string{"uv", "run", "--no-sync", "--with", "mypy==2.4.0", "python"}},
}

type sourceBuild struct {
	app    manifestApp
	entry  string
	recipe recipe.Recipe
}

func (s sourceBuild) plan(prepare [][]string) runtimecontract.Plan {
	return runtimecontract.Plan{Install: runtimecontract.Install{Commands: s.recipe.Deploy.Install}, Prepare: prepare}
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
		entry, err := sourceAppEntry(*app, root)
		if err != nil {
			return err
		}
		steps, err := recipe.Read(os.DirFS(root))
		if err != nil {
			return fmt.Errorf("%s: %w; ship it as an image: app", app.Name, err)
		}
		b.sources[app.Path] = sourceBuild{app: *app, entry: entry, recipe: steps}
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
	runners := filepath.Join(root, ".holon", "tooling", language)
	if err := errors.Join(
		os.CopyFS(root, os.DirFS(source.app.resolved.types.root)), os.CopyFS(runners, os.DirFS(filepath.Join(toolingDir, language))),
	); err != nil {
		return err
	}
	runner := contractRunners[language]
	command := slices.Concat([]string{runner.program, path.Join(".holon", "tooling", language, runner.runner), args[0], "."}, args[2:])
	reply, err := b.build(ctx, source.app.Name, root, source.plan(nil), runtimecontract.Build{Run: command})
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
	if err := errors.Join(writeHolonFile(root, runtimecontract.PlanFile, plan), writeHolonFile(root, runtimecontract.BuildFile, build)); err != nil {
		return runtimecontract.BuildReply{}, err
	}
	_, file, err := packOverlay(ctx, root)
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
		runner := contractRunners[language]
		checker, err := tooling.File(language, runner.checker)
		if err == nil {
			err = writeSourceFile(p.root, path.Join(".holon", runner.checker), checker)
		}
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		build.Run = slices.Concat(runner.check, []string{path.Join(".holon", runner.checker), "check", checked})
	}
	built := options.output.measure(app.Name, "build")
	reply, err := options.build.build(ctx, app.Name, p.root, source.plan(source.recipe.Deploy.Build), build)
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
		development, err := remote.NewRepository(grant.Base)
		if err == nil {
			err = development.Reference.ValidateReferenceAsDigest()
		}
		if err != nil {
			return fmt.Errorf("upload grant names no development image: %w", err)
		}
		development.Client = repository.Client
		ctx := auth.AppendRepositoryScope(ctx, repository.Reference, auth.ActionPull, auth.ActionPush)
		ctx = auth.AppendRepositoryScope(ctx, development.Reference, auth.ActionPull)
		manifest, err := assembleImage(ctx, repository, development, frozen, recipe.Start(source.entry), sourceLabels(app))
		if err != nil {
			return err
		}
		entries := []ocispec.Descriptor{manifest}
		annotations := map[string]string{}
		if p.metadata != nil {
			metadata, ownDigest, err := pushTypeMetadata(ctx, repository, p.metadata, p.documents)
			if err != nil {
				return err
			}
			entries = append(entries, metadata)
			annotations[mcpcontract.DigestAnnotation] = ownDigest
		}
		data, _ := json.Marshal(ocispec.Index{
			Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex,
			Manifests: entries, Annotations: annotations,
		})
		image, err = oras.PushBytes(ctx, repository, ocispec.MediaTypeImageIndex, data)
		return err
	})
	return image, err
}

func assembleImage(
	ctx context.Context, repository, development *remote.Repository, frozen digest.Digest, start []string, labels map[string]string,
) (ocispec.Descriptor, error) {
	var frozenManifest ocispec.Manifest
	if err := fetchReference(ctx, repository, frozen.String(), &frozenManifest); err != nil {
		return ocispec.Descriptor{}, err
	}
	var frozenConfig ocispec.Image
	if err := ociimage.FetchJSON(ctx, repository, frozenManifest.Config, &frozenConfig); err != nil {
		return ocispec.Descriptor{}, err
	}
	missing := fmt.Errorf("the registry does not hold the development image %s yet; it is copied there after each upgrade, so deploy again in a minute", development.Reference)
	var index ocispec.Index
	err := fetchReference(ctx, development, development.Reference.Reference, &index)
	if errors.Is(err, errdef.ErrNotFound) {
		return ocispec.Descriptor{}, missing
	}
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	i := slices.IndexFunc(index.Manifests, func(child ocispec.Descriptor) bool {
		return child.Platform != nil && child.Platform.OS == frozenConfig.OS && child.Platform.Architecture == frozenConfig.Architecture
	})
	if i < 0 {
		return ocispec.Descriptor{}, fmt.Errorf("the development image has no %s/%s image", frozenConfig.OS, frozenConfig.Architecture)
	}
	child := index.Manifests[i]
	data, err := content.FetchAll(ctx, development, child)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ocispec.Descriptor{}, err
	}
	mounts, mountCtx := errgroup.WithContext(ctx)
	for _, layer := range manifest.Layers {
		mounts.Go(func() error {
			return repository.Mount(mountCtx, layer, development.Reference.Repository, func() (io.ReadCloser, error) { return nil, missing })
		})
	}
	if err := mounts.Wait(); err != nil {
		return ocispec.Descriptor{}, err
	}
	assembled, err := ociimage.Edit(ctx, development, repository, child.MediaType, data, func(image *ociimage.Image) error {
		configureImage(image, frozenConfig.Config.WorkingDir, start, labels)
		for i, layer := range frozenManifest.Layers {
			image.AppendLayer(layer, frozenConfig.RootFS.DiffIDs[i], "holon deploy")
		}
		return nil
	})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	assembled.Platform = child.Platform
	return assembled, nil
}

func fetchReference(ctx context.Context, target oras.ReadOnlyTarget, reference string, value any) error {
	_, data, err := oras.FetchBytes(ctx, target, reference, oras.FetchBytesOptions{MaxBytes: 4 << 20})
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func configureImage(image *ociimage.Image, directory string, start []string, labels map[string]string) {
	production := []string{"HOME=/tmp", "NODE_ENV=production", "PYTHONUNBUFFERED=1"}
	environment := slices.DeleteFunc(slices.Clone(image.Config.Env), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return slices.ContainsFunc(production, func(variable string) bool { return strings.HasPrefix(variable, name+"=") })
	})
	merged := map[string]string{}
	maps.Copy(merged, image.Config.Labels)
	maps.Copy(merged, labels)
	for key, value := range map[string]any{
		"Entrypoint": nil, "Cmd": start, "WorkingDir": directory,
		"Env": append(environment, production...), "Labels": merged,
	} {
		image.Runtime[key], _ = json.Marshal(value)
	}
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
