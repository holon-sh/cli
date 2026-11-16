package installation

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"sync"

	"golang.org/x/sync/errgroup"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/holon-sh/protocol"
)

type buildOptions struct {
	importPackage func(context.Context, manifestApp) (protocol.ResolvedAppBuild, error)
	plane         protocol.Plane
	types         *typeWorkspace
	build         *buildSession
	origin        string
	Stderr        io.Writer
	Env           []string
	HTTPClient    *http.Client
	output        *progressOutput
	development   bool
	served        map[string]json.RawMessage
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
		if app.Path != "" && options.development {
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
			if app.resolved.registryPackage != nil {
				build, err := options.importPackage(ctx, app)
				if err != nil {
					return err
				}
				declaration.Image, declaration.Port = build.Image, build.Runtime.Port
				apps[index] = declaration
				return nil
			}
			var image string
			if app.resolved.Image == "" {
				var err error
				if image, err = buildSource(ctx, app, upload, options); err != nil {
					return err
				}
			}
			err := upload.run(ctx, func(grant protocol.UploadGrant) error {
				if image == "" {
					imported, err := importImage(ctx, app.resolved.Image, grant.Registry, grant.Repositories[app.Name], grant.Grant, options.HTTPClient)
					if err != nil {
						return fmt.Errorf("import %s: %w", app.Name, err)
					}
					image = imported
				}
				if app.resolved.stdio {
					prepared, err := prepareStdioImage(ctx, image, grant.Grant, options)
					if err != nil {
						return fmt.Errorf("adapt stdio app %s: %w", app.Name, err)
					}
					image = prepared
				}
				if options.plane != "" && grant.Grant != "" {
					return publishAppBuild(ctx, app, image, grant, options)
				}
				return nil
			})
			if err != nil {
				return err
			}
			declaration.Image = image
			apps[index] = declaration
			return nil
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

func buildManifestImages(
	ctx context.Context, manager *Client, name string, manifest manifestSpec, options buildOptions,
) ([]protocol.App, error) {
	upload := newImageUpload(manager, name, manifest)
	options.importPackage = func(ctx context.Context, app manifestApp) (protocol.ResolvedAppBuild, error) {
		return importRegistryBuild(ctx, manager, ControlPlane(name), app)
	}
	options.origin = manager.client.baseURL
	if name == protocol.ProductionName {
		options.plane = ControlPlane(name)
	}
	return buildAndPush(ctx, manifest, upload, options)
}

func newImageUpload(manager *Client, name string, manifest manifestSpec) *imageUpload {
	return &imageUpload{load: func(ctx context.Context) (protocol.UploadGrant, error) {
		grant, err := imageDestination(ctx, manager, name, manifest)
		if err == nil {
			err = validateUploadGrant(grant, manifest)
		}
		return grant, err
	}}
}

type imageUpload struct {
	mu      sync.Mutex
	load    func(context.Context) (protocol.UploadGrant, error)
	grant   protocol.UploadGrant
	renewed bool
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

func (u *imageUpload) run(ctx context.Context, operation func(protocol.UploadGrant) error) error {
	grant, err := u.destination(ctx, "")
	if err != nil {
		return err
	}
	err = operation(grant)
	var response *errcode.ErrorResponse
	if grant.Grant != "" && errors.As(err, &response) &&
		slices.Contains([]int{http.StatusUnauthorized, http.StatusForbidden}, response.StatusCode) {
		grant, err = u.destination(ctx, grant.Grant)
		if err == nil {
			err = operation(grant)
		}
	}
	return err
}

func (u *imageUpload) publish(ctx context.Context, app string, client *http.Client, operation func(*remote.Repository, protocol.UploadGrant) error) error {
	return u.run(ctx, func(grant protocol.UploadGrant) error {
		repository, err := imageRepository(grant.Registry+"/"+grant.Repositories[app], grant.Grant, client)
		if err != nil {
			return err
		}
		return operation(repository, grant)
	})
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
