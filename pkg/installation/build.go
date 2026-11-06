package installation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/holon-sh/cli/pkg/appartifact"
	"github.com/holon-sh/protocol"
)

type buildOptions struct {
	importPackage func(context.Context, manifestApp) (protocol.ResolvedAppBuild, error)
	catalogTag    string
	types         *typeWorkspace
	metadataGrant protocol.UploadGrant
	resolutions   *sync.Map
	architectures []string
	builder       buildApplication
	builders      *builderConnections
	origin        string
	Stderr        io.Writer
	Env           []string
	HTTPClient    *http.Client
	output        *progressOutput
	check         bool
}

func buildAndPush(ctx context.Context, manifest manifestSpec, upload *imageUpload, options buildOptions) ([]protocol.App, error) {
	stderr := &buildLog{writer: cmp.Or(options.Stderr, io.Discard)}
	apps := make([]protocol.App, len(manifest.Apps))
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(3)
	for index, app := range manifest.Apps {
		declaration := app.resolved.App
		if app.resolved.Remote != nil {
			apps[index] = declaration
			continue
		}
		group.Go(func() (err error) {
			finish := options.output.measure(app.Name, "build and upload")
			defer finish()
			if err := ctx.Err(); err != nil {
				return err
			}
			options := options
			logs := options.output.logs(stderr)
			options.Stderr = logs
			defer func() { logs.finished(options.output, app.Name, "uploaded", err) }()
			action := "building"
			if app.resolved.Image != "" || app.resolved.registryPackage != nil {
				action = "importing"
			}
			options.output.status(app.Name, action, progressWorking)
			if app.resolved.registryPackage != nil {
				imported := options.output.timing().measure(app.Name, "image import")
				build, err := options.importPackage(ctx, app)
				imported()
				if err != nil {
					return err
				}
				declaration.Image, declaration.Port = build.Image, build.Runtime.Port
				apps[index] = declaration
				return nil
			}
			var image string
			if app.resolved.Image == "" {
				image, err = buildSource(ctx, app, upload, options)
				if err != nil {
					return err
				}
			}
			err = upload.run(ctx, func(grant protocol.UploadGrant) error {
				if image == "" {
					finish := options.output.timing().measure(app.Name, "image import")
					imported, err := importImage(ctx, app.resolved.Image, grant.Registry, grant.Repositories[app.Name], grant.Grant, options.HTTPClient)
					finish()
					if err != nil {
						return fmt.Errorf("import %s: %w", app.Name, err)
					}
					image = imported
				}
				if app.resolved.stdio {
					options.output.status(app.Name, "adapting", progressWorking)
					prepared, err := prepareStdioImage(ctx, image, grant.Grant, options)
					if err != nil {
						return fmt.Errorf("adapt stdio app %s: %w", app.Name, err)
					}
					image = prepared
				}
				if options.catalogTag != "" && grant.Grant != "" {
					defer options.output.timing().measure(app.Name, "catalog push")()
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
		for _, app := range apps {
			if app.Name != "" && app.Remote == nil {
				options.output.status(app.Name, "update not applied", progressIdle)
			}
		}
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

func pathInsideContext(path, contextPath string) bool {
	if realPath, err := filepath.EvalSymlinks(path); err == nil {
		path = realPath
	}
	if realContext, err := filepath.EvalSymlinks(contextPath); err == nil {
		contextPath = realContext
	}
	relative, _ := filepath.Rel(contextPath, path)
	return filepath.IsLocal(relative)
}

func buildManifestImages(
	ctx context.Context, manager *Client, name string, manifest manifestSpec, options buildOptions,
) ([]protocol.App, error) {
	upload := &imageUpload{load: func(ctx context.Context) (protocol.UploadGrant, error) {
		defer options.output.timing().measure("", "upload grant")()
		grant, err := imageDestination(ctx, manager, name, manifest)
		if err == nil {
			err = validateUploadGrant(grant, manifest)
		}
		return grant, err
	}}
	options.importPackage = func(ctx context.Context, app manifestApp) (protocol.ResolvedAppBuild, error) {
		return importRegistryBuild(ctx, manager, ControlPlane(name), app, options.output)
	}
	options.origin = manager.client.baseURL
	options.catalogTag = appartifact.DevelopmentTag
	if name == protocol.ProductionName {
		options.catalogTag = appartifact.ReleasePrefix
	}
	return buildAndPush(ctx, manifest, upload, options)
}

type imageUpload struct {
	mu           sync.Mutex
	load         func(context.Context) (protocol.UploadGrant, error)
	grant        protocol.UploadGrant
	renewed      bool
	repositories sync.Map
	blobs        sync.Map
	blobUploads  singleflight.Group
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
