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

// These belong in the protocol module once the source overlay leaves v0.
const (
	toolReloadApp    = "reload_app"
	overlayMediaType = "application/vnd.holon.source-overlay.v0.tar+zstd"
)

const overlayExtension = ".tar.zst"

type reloadAppRequest struct {
	protocol.AppRequest

	Digest string `json:"digest"`
}

type reloadAppResult struct {
	ElapsedSeconds float64 `json:"elapsedSeconds"` // how long the API waited, from fetching the source to the app being ready
}

// preparation is what the work behind a save produced: the contracts it prepared and any images it built.
type preparation struct {
	contexts sets.Set[string] // the apps the save changed
	prepared manifestSpec
	built    manifestSpec // the apps it built images for, and their images
	images   []protocol.App
	err      error
	buildErr error
}

// prepare starts the work behind a save without delaying the next one: contract preparation, then new images
// for the apps marked changed. The loop accepts what it produces, and the next change stops it.
func (d *devLoop) prepare(ctx context.Context, contexts sets.Set[string]) {
	background, cancel := context.WithCancel(ctx)
	result := make(chan preparation, 1)
	manifest, options, changed := d.manifest, d.options, d.changedImages.Clone()
	go func() { result <- d.refresh(background, contexts, manifest, options, changed) }()
	d.preparing, d.cancelPreparation = result, cancel
}

func (d *devLoop) refresh(ctx context.Context, contexts sets.Set[string], manifest manifestSpec, options buildOptions, changed sets.Set[string]) preparation {
	prepared, cleanup, err := prepareManifestTypes(ctx, manifest, options)
	if err != nil {
		return preparation{contexts: contexts, err: err}
	}
	defer cleanup()
	result := preparation{contexts: contexts, prepared: prepared, built: prepared}
	result.built.Apps = slices.DeleteFunc(slices.Clone(prepared.Apps), func(app manifestApp) bool {
		return !changed.Has(app.Name) && !options.types.affected(ctx, app, prepared)
	})
	if len(result.built.Apps) == 0 {
		return result
	}
	options.output.status("", "Preparing apps", progressWorking)
	result.images, result.buildErr = buildManifestImages(ctx, d.manager, d.name, result.built, options)
	if result.buildErr == nil {
		result.buildErr = currentTypeSources(prepared)
	}
	return result
}

// reload restarts each app in contexts from an overlay of its source and marks those whose image the change
// outdated. A failed restart is reported and leaves the app running what it ran.
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

// stopPreparation cancels the work behind the last save and waits for it, dropping what it produced, and
// returns the apps that save changed, which the next preparation owes.
func (d *devLoop) stopPreparation() sets.Set[string] {
	if d.preparing == nil {
		return sets.New[string]()
	}
	d.cancelPreparation()
	stopped := <-d.preparing
	d.preparing = nil
	return stopped.contexts
}

// acceptPreparation accepts the contracts the work behind a save prepared and rolls out the images it built.
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

// reloadApp restarts app from an overlay of its current source, staged with the clients of the last accepted
// build, and reports whether its image no longer matches what it is built from. An app outside the launcher
// has no overlay, so only a new image can carry its source.
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

// stableStat clears what differs between two snapshots of the same source.
func stableStat(_ string, s *fstypes.Stat) fsutil.MapResult {
	s.ModTime, s.Uid, s.Gid, s.Xattrs = 0, 0, 0, nil
	return fsutil.MapResultKeep
}

// writeOverlay archives the staged snapshot beside it, with a stable file order and no timestamps or owners, so
// that an unchanged snapshot yields the same digest.
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
