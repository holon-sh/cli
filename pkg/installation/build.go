package installation

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/holon-sh/cli/pkg/appartifact"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

type buildOptions struct {
	manager    *Client
	host       protocol.Host
	session    string
	build      *buildSession
	Env        []string
	HTTPClient *http.Client
	output     *progressOutput
}

func buildAndPush(ctx context.Context, manifest manifestSpec, upload *imageUpload, options buildOptions) ([]protocol.App, error) {
	apps := make([]protocol.App, len(manifest.Apps))
	group, ctx := errgroup.WithContext(ctx)
	for index, app := range manifest.Apps {
		declaration := app.resolved.App
		if app.resolved.Remote != nil {
			apps[index] = declaration
			continue
		}
		if app.Path != "" && options.host == protocol.DevHost {
			if _, err := sourceAppEntry(app, app.resolved.types.root); err != nil {
				return nil, err
			}
			declaration.Port, declaration.Source = 0, true
			apps[index] = declaration
			continue
		}
		group.Go(func() error {
			finish := options.output.measure(app.Name, "build and upload")
			defer finish()
			if err := ctx.Err(); err != nil {
				return err
			}
			if app.resolved.pkg != nil {
				build, err := importRegistryBuild(ctx, options.manager, options.host, app)
				if err != nil {
					return err
				}
				declaration.Image, declaration.Port = build.Image, build.Runtime.Port
				apps[index] = declaration
				return nil
			}
			var built ocispec.Descriptor
			if app.resolved.Image == "" {
				var err error
				if built, err = buildSource(ctx, app, upload, options); err != nil {
					return err
				}
			}
			return upload.repository(ctx, app.Name, func(repository *remote.Repository, grant protocol.UploadGrant) (err error) {
				image := built
				if app.resolved.Image != "" {
					if image, err = importImage(ctx, app.resolved.Image, repository, upload.client); err != nil {
						return fmt.Errorf("import %s: %w", app.Name, err)
					}
				}
				if app.resolved.stdio {
					if image, err = adaptStdio(ctx, repository, image, options.manager.client.baseURL); err != nil {
						return fmt.Errorf("adapt stdio app %s: %w", app.Name, err)
					}
				}
				if options.host == protocol.AdminHost && grant.Grant != "" {
					ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
					defer cancel()
					record := mcpregistry.BuildRecord(app.resolved.App, app.resolved.catalogEnv, app.resolved.descriptor)
					if _, err = appartifact.Publish(ctx, repository, image, record, options.host); err != nil {
						return err
					}
				}
				declaration.Image = repository.Reference.String() + "@" + image.Digest.String()
				apps[index] = declaration
				return nil
			})
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return apps, nil
}

func validateUploadGrant(grant protocol.UploadGrant, manifest manifestSpec) error {
	if !protocol.ValidRegistryHost(grant.Registry) {
		return errors.New("upload grant has an invalid registry")
	}
	if grant.Grant != "" && !validToken(grant.Grant) {
		return errors.New("upload grant is invalid")
	}
	if i := slices.IndexFunc(manifest.Apps, func(app manifestApp) bool { return !protocol.ValidRepository(grant.Repositories[app.Name]) }); i >= 0 {
		return fmt.Errorf("upload grant has an invalid repository for %s", manifest.Apps[i].Name)
	}
	return nil
}

func regularNoSymlink(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file, not a symlink or directory: %s", label, path)
	}
	return nil
}

func newImageUpload(options buildOptions, manifest manifestSpec) *imageUpload {
	return &imageUpload{client: options.HTTPClient, load: func(ctx context.Context) (protocol.UploadGrant, error) {
		grant, err := imageDestination(ctx, options.manager, options.host, manifest)
		if err == nil {
			err = validateUploadGrant(grant, manifest)
		}
		return grant, err
	}}
}

type imageUpload struct {
	mu           sync.Mutex
	client       *http.Client
	load         func(context.Context) (protocol.UploadGrant, error)
	grant        protocol.UploadGrant
	renewed      bool
	repositories sync.Map
}

func (u *imageUpload) destination(ctx context.Context, rejected string) (protocol.UploadGrant, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.grant.Registry == "" || rejected != "" && rejected == u.grant.Grant && !u.renewed {
		grant, err := u.load(ctx)
		if err != nil {
			return protocol.UploadGrant{}, err
		}
		if u.grant.Registry != "" {
			if grant.Registry != u.grant.Registry || !maps.Equal(grant.Repositories, u.grant.Repositories) {
				return protocol.UploadGrant{}, errors.New("upload destination changed during renewal")
			}
			u.renewed = true
		}
		u.grant = grant
	}
	return u.grant, nil
}

func (u *imageUpload) repository(ctx context.Context, app string, operation func(*remote.Repository, protocol.UploadGrant) error) error {
	attempt := func(grant protocol.UploadGrant) error {
		name := grant.Registry + "/" + grant.Repositories[app]
		key := name + " " + grant.Grant
		repository, ok := u.repositories.Load(key)
		if !ok {
			created, err := imageRepository(name, grant.Grant, u.client)
			if err != nil {
				return err
			}
			repository, _ = u.repositories.LoadOrStore(key, created)
		}
		return operation(repository.(*remote.Repository), grant)
	}
	grant, err := u.destination(ctx, "")
	if err != nil {
		return err
	}
	err = attempt(grant)
	var response *errcode.ErrorResponse
	if grant.Grant != "" && errors.As(err, &response) &&
		slices.Contains([]int{http.StatusUnauthorized, http.StatusForbidden}, response.StatusCode) {
		grant, err = u.destination(ctx, grant.Grant)
		if err == nil {
			err = attempt(grant)
		}
	}
	return err
}
