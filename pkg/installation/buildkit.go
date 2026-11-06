package installation

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	contentlocal "github.com/containerd/containerd/v2/plugins/content/local"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/containerd/log"
	dockerconfig "github.com/docker/cli/cli/config/types"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/moby/buildkit/util/staticfs"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"
	orascontent "oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
	registryauth "oras.land/oras-go/v2/registry/remote/auth"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/internal/kiln"
	"github.com/holon-sh/cli/pkg/ociimage"
	"github.com/holon-sh/protocol"
)

func publishSource(ctx context.Context, request buildRequest, progress io.Writer) (buildResult, error) {
	planned := request.output.measure(request.app.Name, "plan")
	plan, err := kiln.Plan(ctx, request.Request)
	planned()
	if err != nil {
		return buildResult{}, err
	}
	source, err := fsutil.NewFS(request.Source)
	if err != nil {
		return buildResult{}, err
	}
	manifests := make([]ocispec.Descriptor, len(request.Platforms))
	group, buildCtx := errgroup.WithContext(ctx)
	for index, platform := range request.Platforms {
		group.Go(func() error {
			manifest, err := publishPlatform(buildCtx, platform, request, source, plan.Dockerfile, progress)
			manifests[index] = manifest
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return buildResult{}, err
	}
	slices.SortFunc(manifests, func(a, b ocispec.Descriptor) int {
		return strings.Compare(a.Platform.Architecture, b.Platform.Architecture)
	})
	var image string
	err = request.publish(ctx, func(repository *remote.Repository, _ protocol.UploadGrant) error {
		entries := slices.Clone(manifests)
		var annotations map[string]string
		if p := request.app.resolved.types; p.metadata != nil {
			finish := request.output.timing().measure(request.app.Name, "contract push")
			metadata, ownDigest, err := pushTypeMetadata(ctx, repository, p.metadata, p.documents)
			finish()
			if err != nil {
				return err
			}
			entries = append(entries, metadata)
			annotations = map[string]string{mcpcontract.DigestAnnotation: ownDigest}
		}
		data, _ := json.Marshal(ocispec.Index{
			Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex,
			Manifests: entries, Annotations: annotations,
		})
		defer request.output.timing().measure(request.app.Name, "index push")()
		descriptor, err := oras.PushBytes(ctx, repository, ocispec.MediaTypeImageIndex, data)
		if err == nil {
			image = repository.Reference.String() + "@" + descriptor.Digest.String()
		}
		return err
	})
	return buildResult{Image: image, Resolution: plan.Resolution}, err
}

var quietContentStore = sync.OnceFunc(func() { _ = log.SetLevel("error") })

type builderConnections struct {
	clients sync.Map
}

type builderConnection struct {
	*client.Client

	once      sync.Once
	err       error
	ctx       context.Context //nolint:containedctx
	stop      context.CancelCauseFunc
	processes sync.WaitGroup
}

func (b *builderConnections) connect(platform string, env []string) (*builderConnection, error) {
	value, _ := b.clients.LoadOrStore(platform, new(builderConnection))
	connection := value.(*builderConnection)
	connection.once.Do(func() {
		connection.ctx, connection.stop = context.WithCancelCause(context.Background())
		connection.Client, connection.err = client.New(connection.ctx, "", client.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			local, remote := net.Pipe()
			connection.processes.Go(func() {
				defer local.Close()
				defer remote.Close()
				stop := context.AfterFunc(connection.ctx, func() { local.Close(); remote.Close() })
				defer stop()
				var diagnostic bytes.Buffer
				builder, err := sourceBuilder(connection.ctx, env)
				if err == nil {
					err = commandenv.RunStreaming(connection.ctx, "docker", []string{"buildx", "dial-stdio", "--builder", builder, "--platform", platform},
						local, env, local, &diagnostic)
				}
				if err != nil {
					connection.stop(fmt.Errorf("connect to builder: %w: %s", err, diagnostic.String()))
					b.clients.CompareAndDelete(platform, connection)
					_ = connection.Close()
				}
			})
			return remote, nil
		}))
	})
	return connection, connection.err
}

func (b *builderConnections) close() {
	b.clients.Range(func(_, value any) bool {
		connection := value.(*builderConnection)
		connection.stop(context.Canceled)
		_ = connection.Close()
		connection.processes.Wait()
		return true
	})
}

func publishPlatform(
	ctx context.Context, platform string, request buildRequest, source fsutil.FS, dockerfile string, progress io.Writer,
) (ocispec.Descriptor, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)
	recipe := staticfs.NewFS()
	recipe.Add("Dockerfile", &fstypes.Stat{Mode: 0o600}, []byte(dockerfile))
	var runtimeKey string
	if len(request.Start) != 0 {
		arch := strings.TrimPrefix(platform, "linux/")
		binary, err := bridgePayloads.ReadFile("bridge-payloads/linux-" + arch)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		recipe.Add(arch, &fstypes.Stat{Mode: 0o755}, binary)
		runtimeKey = clientutil.HashBytes(binary)
	}
	name := request.app.Name + " " + platform
	connected := request.output.measure(name, "connect builder")
	c, err := request.builders.connect(platform, request.Env)
	connected()
	if err != nil {
		request.builders.clients.CompareAndDelete(platform, c)
		return ocispec.Descriptor{}, err
	}
	credentials, err := imageRepository("registry-1.docker.io/library/unused", "", request.httpClient)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	authentication := authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
		AuthConfigProvider: func(ctx context.Context, host string, _ []string, _ authprovider.ExpireCachedAuthCheck) (dockerconfig.AuthConfig, error) {
			credential, err := credentials.Client.(*registryauth.Client).Credential(ctx, host)
			return dockerconfig.AuthConfig{
				Username: credential.Username, Password: credential.Password,
				IdentityToken: credential.RefreshToken, RegistryToken: credential.AccessToken,
			}, err
		},
	})
	quietContentStore()
	directory, err := os.MkdirTemp("", "holon-export-*")
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer os.RemoveAll(directory)
	store, err := contentlocal.NewStore(directory)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	statuses := make(chan *client.SolveStatus)
	display, _ := progressui.NewDisplay(progressFunc(func(data []byte) (int, error) {
		n, err := progress.Write(data)
		if err != nil {
			cancel(err)
		}
		return n, err
	}), progressui.PlainMode, progressui.WithDesc(name, ""))
	var renderer sync.WaitGroup
	renderer.Go(func() { _, _ = display.UpdateFrom(context.WithoutCancel(ctx), statuses) })
	hostname, _ := os.Hostname()
	solved := request.output.measure(name, "solve")
	response, err := c.Build(ctx, client.SolveOpt{
		SharedKey:   clientutil.HashBytes([]byte(hostname + "\x00" + platform)),
		LocalMounts: map[string]fsutil.FS{"context": source, "dockerfile": recipe, "runtime": recipe},
		Session:     []session.Attachable{authentication},
		Exports: []client.ExportEntry{{Type: client.ExporterOCI, OutputStore: &sourceExport{Store: store, request: request}, Attrs: map[string]string{
			"tar": "false", "source-date-epoch": "0", "oci-mediatypes": "true", "compression": "zstd", "compression-level": "1",
		}}},
	}, "holon", func(ctx context.Context, g gateway.Client) (*gateway.Result, error) {
		input, err := llb.Local("context", llb.SharedKeyHint(clientutil.HashBytes([]byte(request.app.Path)))).Marshal(ctx)
		if err != nil {
			return nil, err
		}
		result, err := g.Solve(ctx, gateway.SolveRequest{Frontend: "dockerfile.v0", FrontendOpt: map[string]string{
			"platform": platform, "build-arg:SOURCE_DATE_EPOCH": "0",
		}, FrontendInputs: map[string]*pb.Definition{"context": input.ToPB()}, Evaluate: true})
		if err != nil {
			return nil, err
		}
		var image ocispec.Image
		if err := json.Unmarshal(result.Metadata[exptypes.ExporterImageConfigKey], &image); err != nil {
			return nil, err
		}
		if image.OS+"/"+image.Architecture != platform {
			return nil, errors.New("build returned an unexpected image platform")
		}
		image.Config.User = "10001:10001"
		labels := map[string]string{}
		maps.Copy(labels, image.Config.Labels)
		maps.Copy(labels, sourceLabels(request.app))
		image.Config.Labels = labels
		if len(request.Start) != 0 {
			state, err := result.Ref.ToState()
			if err != nil {
				return nil, err
			}
			runtime := llb.Scratch().File(llb.Copy(
				llb.Local("runtime", llb.IncludePatterns([]string{image.Architecture}), llb.SharedKeyHint(runtimeKey)), image.Architecture, ociimage.RuntimePath,
				llb.WithCreatedTime(time.Unix(0, 0)),
			))
			definition, err := llb.Merge([]llb.State{state, runtime}).Marshal(ctx, llb.Platform(image.Platform))
			if err != nil {
				return nil, err
			}
			hosted, err := g.Solve(ctx, gateway.SolveRequest{Definition: definition.ToPB(), Evaluate: true})
			if err != nil {
				return nil, err
			}
			result.SetRef(hosted.Ref)
			image.Config.Entrypoint = append([]string{ociimage.RuntimePath, "--transport=hosted-http", "--"}, request.Start...)
			image.Config.Cmd, image.Config.StopSignal = nil, "SIGTERM"
			image.History = append(image.History, ocispec.History{CreatedBy: "holon application host"})
		}
		data, _ := json.Marshal(image)
		result.AddMeta(exptypes.ExporterImageConfigKey, data)
		return result, nil
	}, request.output.timing().solveStatuses(name, statuses))
	renderer.Wait()
	solved()
	if err := cmp.Or(context.Cause(ctx), context.Cause(c.ctx), err); err != nil {
		return ocispec.Descriptor{}, err
	}
	data, err := base64.StdEncoding.DecodeString(response.ExporterResponse[exptypes.ExporterImageDescriptorKey])
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	var descriptor ocispec.Descriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return ocispec.Descriptor{}, err
	}
	if descriptor.MediaType != ocispec.MediaTypeImageManifest || !protocol.ValidDigest(descriptor.Digest.String()) || descriptor.Size <= 0 {
		return ocispec.Descriptor{}, errors.New("build did not export a SHA-256 OCI manifest")
	}
	staged, err := oci.New(directory)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	copyOptions := oras.DefaultCopyGraphOptions
	copyOptions.FindSuccessors = func(ctx context.Context, fetcher orascontent.Fetcher, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		successors, err := orascontent.Successors(ctx, fetcher, node)
		return slices.DeleteFunc(successors, func(successor ocispec.Descriptor) bool {
			written, _ := staged.Exists(ctx, successor)
			return !written
		}), err
	}
	pushed := request.output.measure(name, "push")
	defer pushed()
	if err := request.publish(ctx, func(repository *remote.Repository, grant protocol.UploadGrant) error {
		copyOptions.PreCopy = func(ctx context.Context, descriptor ocispec.Descriptor) error {
			if len(grant.Repositories) < 2 || grant.Grant == "" ||
				(descriptor.MediaType != ocispec.MediaTypeImageLayer && descriptor.MediaType != ocispec.MediaTypeImageLayerGzip && descriptor.MediaType != ocispec.MediaTypeImageLayerZstd) {
				return nil
			}
			if err := request.upload.pushLayer(ctx, repository, descriptor, staged); err != nil {
				return err
			}
			return oras.SkipNode
		}
		return oras.CopyGraph(ctx, staged, repository, descriptor, copyOptions)
	}); err != nil {
		return ocispec.Descriptor{}, err
	}
	descriptor.Platform = &ocispec.Platform{OS: "linux", Architecture: strings.TrimPrefix(platform, "linux/")}
	return descriptor, nil
}

func (u *imageUpload) pushLayer(ctx context.Context, repository *remote.Repository, descriptor ocispec.Descriptor, source orascontent.Fetcher) error {
	var pushed bool
	result := u.blobUploads.DoChan(descriptor.Digest.String(), func() (any, error) {
		if source, ok := u.blobs.Load(descriptor.Digest); ok {
			return source, nil
		}
		reader, err := source.Fetch(ctx, descriptor)
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		if err := repository.Push(ctx, descriptor, reader); err != nil {
			return nil, err
		}
		u.blobs.Store(descriptor.Digest, repository.Reference.Repository)
		pushed = true
		return repository.Reference.Repository, nil
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case outcome := <-result:
		if outcome.Err != nil || pushed {
			return outcome.Err
		}
		return repository.Mount(ctx, descriptor, outcome.Val.(string), func() (io.ReadCloser, error) {
			return source.Fetch(ctx, descriptor)
		})
	}
}

func (r buildRequest) publish(ctx context.Context, operation func(*remote.Repository, protocol.UploadGrant) error) error {
	return r.upload.run(ctx, func(grant protocol.UploadGrant) error {
		name := grant.Registry + "/" + grant.Repositories[r.app.Name]
		key := name + " " + grant.Grant
		repository, ok := r.upload.repositories.Load(key)
		if !ok {
			created, err := imageRepository(name, grant.Grant, r.httpClient)
			if err != nil {
				return err
			}
			repository, _ = r.upload.repositories.LoadOrStore(key, created)
		}
		return operation(repository.(*remote.Repository), grant)
	})
}

type sourceExport struct {
	content.Store

	request buildRequest
}

func (s *sourceExport) Writer(ctx context.Context, options ...content.WriterOpt) (content.Writer, error) {
	var settings content.WriterOpts
	for _, option := range options {
		if err := option(&settings); err != nil {
			return nil, err
		}
	}
	if err := s.request.publish(ctx, func(repository *remote.Repository, _ protocol.UploadGrant) error {
		exists, err := repository.Blobs().Exists(ctx, settings.Desc)
		if err == nil && exists {
			s.request.upload.blobs.LoadOrStore(settings.Desc.Digest, repository.Reference.Repository)
			return cerrdefs.ErrAlreadyExists
		}
		return err
	}); err != nil {
		return nil, err
	}
	return s.Store.Writer(ctx, options...)
}

func sourceBuilder(ctx context.Context, env []string) (string, error) {
	if name := commandenv.Value(env, "BUILDX_BUILDER"); name != "" {
		return name, nil
	}
	run := func(args ...string) (string, error) {
		output, err := commandenv.Run(ctx, "docker", args, nil, env)
		return strings.TrimSpace(output), err
	}
	endpoint, driver, prefix := commandenv.Value(env, "BUILDKIT_HOST"), "remote", "kiln-remote-"
	if endpoint == "" {
		driver, prefix = "docker-container", "kiln-"
		endpoint = cmp.Or(commandenv.Value(env, "DOCKER_CONTEXT"), commandenv.Value(env, "DOCKER_HOST"))
		if endpoint == "" {
			var err error
			endpoint, err = run("context", "inspect", "--format", "{{.Name}}")
			if err != nil {
				return "", err
			}
		}
	}
	name := prefix + clientutil.HashBytes([]byte(endpoint))[:12]
	if _, err := run("buildx", "inspect", name); err != nil {
		if _, err := run("buildx", "create", "--name", name, "--driver", driver, endpoint); err != nil {
			if _, inspectErr := run("buildx", "inspect", name); inspectErr != nil {
				return "", err
			}
		}
	}
	return name, nil
}
