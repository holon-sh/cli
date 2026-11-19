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

	"github.com/holon-sh/cli/pkg/appartifact"
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
	program string
	check   []string
}{
	"typescript": {"node", []string{"node"}},
	"python":     {".venv/bin/python", []string{"uv", "run", "--no-sync", "--with", "mypy==2.4.0", "python"}},
}

func (s sourceApp) plan(prepare [][]string) runtimecontract.Plan {
	return runtimecontract.Plan{Install: runtimecontract.Install{Commands: s.recipe.Deploy.Install}, Prepare: prepare}
}

type buildSession struct {
	manager *Client
	output  *progressOutput
	session protocol.SessionResponse
	sources map[string]sourceApp
}

func (b *buildSession) start(ctx context.Context, apps []*manifestApp) error {
	if len(apps) == 0 {
		return nil
	}
	if b.manager.info.ImageRepositoryPrefix != "" {
		return fmt.Errorf("%s: holon deploy builds source apps in the installation's registry, which this installation turns off; ship it as an image: app", apps[0].Name)
	}
	b.sources = map[string]sourceApp{}
	var request protocol.AppsRequest
	for _, app := range apps {
		source, err := readSource(*app, app.resolved.types.root, b.output)
		if err != nil {
			return err
		}
		b.sources[app.Path] = source
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
		development, err := remote.NewRepository(grant.Registry + "/" + protocol.BaseRepository)
		if err != nil {
			return err
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
	base, err := digest.Parse(frozenManifest.Annotations[ocispec.AnnotationBaseImageDigest])
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("the image the build pushed has no %s annotation naming its base image", ocispec.AnnotationBaseImageDigest)
	}
	var frozenConfig ocispec.Image
	if err := ociimage.FetchJSON(ctx, repository, frozenManifest.Config, &frozenConfig); err != nil {
		return ocispec.Descriptor{}, err
	}
	named := development.Reference
	named.Reference = base.String()
	missing := fmt.Errorf("the registry does not hold the development image %s yet; it is copied there after each upgrade, so deploy again in a minute", named)
	var index ocispec.Index
	err = fetchReference(ctx, development, named.Reference, &index)
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
	assembled, err := ociimage.Edit(ctx, development, repository, child.MediaType, data, func(image *ociimage.Image) error {
		mounts, mountCtx := errgroup.WithContext(ctx)
		for _, layer := range image.Layers {
			mounts.Go(func() error {
				return repository.Mount(mountCtx, layer, development.Reference.Repository, func() (io.ReadCloser, error) { return nil, missing })
			})
		}
		configureImage(image, frozenConfig.Config.WorkingDir, start, labels)
		for i, layer := range frozenManifest.Layers {
			image.AppendLayer(layer, frozenConfig.RootFS.DiffIDs[i], appartifact.FrozenBy)
		}
		return mounts.Wait()
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
