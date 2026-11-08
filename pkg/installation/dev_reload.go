package installation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/holon-sh/protocol"
)

const (
	toolReloadApp    = "reload_app"
	overlayMediaType = "application/vnd.holon.source-overlay.v0.tar+zstd"
	overlayExtension = ".tar.zst"
)

type reloadAppRequest struct {
	protocol.AppRequest

	Digest string `json:"digest"`
}

type reloadAppResult struct {
	ElapsedSeconds float64 `json:"elapsedSeconds"`
}

type preparation struct {
	contexts sets.Set[string]
	prepared manifestSpec
	built    manifestSpec
	images   []protocol.App
	err      error
	buildErr error
}

func (d *devLoop) prepare(ctx context.Context, contexts sets.Set[string]) {
	ctx, cancel := context.WithCancel(ctx)
	result := make(chan preparation, 1)
	d.preparing, d.cancelPreparation = result, cancel
	manifest, options, changed := d.manifest, d.options, d.changedImages.Clone()
	go func() {
		prepared, cleanup, err := prepareManifestTypes(ctx, manifest, options)
		if err != nil {
			result <- preparation{contexts: contexts, err: err}
			return
		}
		defer cleanup()
		next := preparation{contexts: contexts, prepared: prepared, built: prepared}
		next.built.Apps = slices.DeleteFunc(slices.Clone(prepared.Apps), func(app manifestApp) bool {
			return !changed.Has(app.Name) && !options.types.affected(ctx, app, prepared)
		})
		if len(next.built.Apps) != 0 {
			options.output.status("", "Preparing apps", progressWorking)
			next.images, next.buildErr = buildManifestImages(ctx, d.manager, d.name, next.built, options)
			if next.buildErr == nil {
				next.buildErr = currentTypeSources(prepared)
			}
		}
		result <- next
	}()
}

func (d *devLoop) reload(ctx context.Context, contexts sets.Set[string]) {
	for _, app := range d.manifest.Apps {
		if !contexts.Has(app.Path) {
			continue
		}
		outdated, err := d.reloadApp(ctx, app)
		if err != nil {
			d.options.output.status(app.Name, fmt.Sprintf("reload failed: %v", err), progressError)
		}
		if outdated {
			d.changedImages.Insert(app.Name)
		}
	}
}

func (d *devLoop) stopPreparation() sets.Set[string] {
	if d.preparing == nil {
		return sets.New[string]()
	}
	d.cancelPreparation()
	stopped := <-d.preparing
	d.preparing = nil
	return stopped.contexts
}

func (d *devLoop) acceptPreparation(ctx context.Context, result preparation) error {
	d.cancelPreparation()
	d.preparing = nil
	if ctx.Err() != nil {
		return nil
	}
	d.options.output.problem("MCP types", result.err)
	if result.err != nil {
		return nil
	}
	for _, app := range result.prepared.Apps {
		if result.contexts.Has(app.Path) {
			types := *app.resolved.types
			types.inputs = d.options.types.accepted[app.Name].inputs
			d.options.types.accepted[app.Name] = &types
		}
	}
	if len(result.built.Apps) == 0 {
		return nil
	}
	if result.buildErr != nil {
		return fmt.Errorf("remote image preparation failed: %w", result.buildErr)
	}
	return d.rollOut(ctx, result)
}

func (d *devLoop) reloadApp(ctx context.Context, app manifestApp) (outdated bool, err error) {
	defer d.options.output.measure(app.Name, "reload")()
	directory, err := os.MkdirTemp("", "holon-overlay-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	snapshot := filepath.Join(directory, "source")
	if _, err := snapshotSource(app, snapshot); err != nil {
		return false, err
	}
	accepted := d.options.types.accepted[app.Name]
	types := *accepted
	types.root = snapshot
	plan, err := d.options.planSource(ctx, app, &types)
	if err != nil {
		return false, err
	}
	if !plan.Development || accepted.inputs == "" {
		return true, nil
	}
	return types.inputs != accepted.inputs, d.restart(ctx, app, plan)
}

func (d *devLoop) restart(ctx context.Context, app manifestApp, plan buildRequest) error {
	d.options.output.status(app.Name, "reloading", progressWorking)
	overlay, err := plan.writeOverlay(ctx)
	if err != nil {
		return err
	}
	if overlay.Digest.String() == d.reloaded[app.Name] {
		d.options.output.status(app.Name, "unchanged", progressDone)
		return nil
	}
	plan.upload = newImageUpload(d.manager, d.name, manifestSpec{Apps: []manifestApp{app}})
	if err := plan.pushOverlay(ctx, overlay); err != nil {
		return err
	}
	var result reloadAppResult
	request := reloadAppRequest{
		AppRequest: protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: d.name}, App: app.Name},
		Digest:     overlay.Digest.String(),
	}
	if err := d.manager.Call(ctx, ControlPlane(d.name), toolReloadApp, request, &result); err != nil {
		return err
	}
	d.reloaded[app.Name] = request.Digest
	d.options.output.status(app.Name, fmt.Sprintf("reloaded in %.1f s", result.ElapsedSeconds), progressDone)
	return nil
}

func stableStat(_ string, s *fstypes.Stat) fsutil.MapResult {
	s.ModTime, s.Uid, s.Gid, s.Xattrs = 0, 0, 0, nil
	return fsutil.MapResultKeep
}

func (r buildRequest) writeOverlay(ctx context.Context) (descriptor ocispec.Descriptor, err error) {
	source, err := fsutil.NewFS(r.Source)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	stable, err := fsutil.NewFilterFS(source, &fsutil.FilterOpt{Map: stableStat})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	file, err := os.Create(r.Source + overlayExtension)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	digester := digest.SHA256.Digester()
	compressed, err := zstd.NewWriter(io.MultiWriter(file, digester.Hash()), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := errors.Join(fsutil.WriteTar(ctx, stable, compressed), compressed.Close()); err != nil {
		return ocispec.Descriptor{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return ocispec.Descriptor{MediaType: overlayMediaType, Digest: digester.Digest(), Size: info.Size()}, nil
}

func (r buildRequest) pushOverlay(ctx context.Context, overlay ocispec.Descriptor) error {
	err := r.publish(ctx, func(repository *remote.Repository, _ protocol.UploadGrant) error {
		file, err := os.Open(r.Source + overlayExtension)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		return repository.Blobs().Push(ctx, overlay, file)
	})
	if err != nil {
		return fmt.Errorf("upload source: %w", err)
	}
	return nil
}
