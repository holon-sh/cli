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
	"github.com/moby/buildkit/client"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/staticfs"
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

func (d *devLoop) reloadApp(ctx context.Context, app manifestApp) (outdated bool, err error) {
	defer d.options.output.measure(app.Name, "reload")()
	directory, err := os.MkdirTemp("", "holon-overlay-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	accepted := d.options.types.accepted[app.Name]
	types := *accepted
	types.root = filepath.Join(directory, "source")
	if _, err := snapshotSource(app, types.root); err != nil {
		return false, err
	}
	plan, err := d.options.planSource(ctx, app, &types)
	if err != nil {
		return false, err
	}
	if !plan.Development || accepted.inputs == "" {
		return true, nil
	}
	outdated = types.inputs != accepted.inputs
	d.options.output.status(app.Name, "reloading", progressWorking)
	running := &d.session.Apps[slices.IndexFunc(d.session.Apps, func(current protocol.App) bool { return current.Name == app.Name })]
	plan.upload = newImageUpload(d.manager, d.name, manifestSpec{Apps: []manifestApp{app}})
	logs := d.options.output.logs(d.options.Stderr)
	overlay, err := plan.pushOverlay(ctx, running.Overlay, logs)
	if err != nil {
		logs.failed(app.Name)
		return outdated, err
	}
	if overlay == running.Overlay {
		d.options.output.status(app.Name, "unchanged", progressDone)
		return outdated, nil
	}
	var result reloadAppResult
	request := reloadAppRequest{
		AppRequest: protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: d.name}, App: app.Name},
		Digest:     overlay,
	}
	if err := d.manager.Call(ctx, ControlPlane(d.name), toolReloadApp, request, &result); err != nil {
		return outdated, err
	}
	running.Overlay = overlay
	d.options.output.status(app.Name, fmt.Sprintf("reloaded in %.1f s", result.ElapsedSeconds), progressDone)
	return outdated, nil
}

func stableStat(_ string, s *fstypes.Stat) fsutil.MapResult {
	s.ModTime, s.Uid, s.Gid, s.Xattrs = 0, 0, 0, nil
	return fsutil.MapResultKeep
}

func (r buildRequest) pushOverlay(ctx context.Context, unless string, progress io.Writer) (string, error) {
	source, err := fsutil.NewFS(r.Source)
	if err != nil {
		return "", err
	}
	if r.plan.Output != "" {
		recipe := staticfs.NewFS()
		recipe.Add("Dockerfile", &fstypes.Stat{Mode: 0o600}, []byte(r.plan.Output))
		local := client.ExportEntry{Type: client.ExporterLocal, OutputDir: r.Source}
		if _, err := r.solve(ctx, r.Platforms[0], recipe, source, progress, local, func(context.Context, gateway.Client, *gateway.Result) error { return nil }); err != nil {
			return "", err
		}
	}
	stable, err := fsutil.NewFilterFS(source, &fsutil.FilterOpt{Map: stableStat})
	if err != nil {
		return "", err
	}
	file, err := os.Create(r.Source + ".tar.zst")
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	digester := digest.SHA256.Digester()
	compressed, err := zstd.NewWriter(io.MultiWriter(file, digester.Hash()), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return "", err
	}
	if err := errors.Join(fsutil.WriteTar(ctx, stable, compressed), compressed.Close()); err != nil {
		return "", err
	}
	overlay := ocispec.Descriptor{MediaType: overlayMediaType, Digest: digester.Digest()}
	if overlay.Digest.String() == unless {
		return unless, nil
	}
	if overlay.Size, err = file.Seek(0, io.SeekCurrent); err != nil {
		return "", err
	}
	err = r.publish(ctx, func(repository *remote.Repository, _ protocol.UploadGrant) error {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return repository.Blobs().Push(ctx, overlay, file)
	})
	if err != nil {
		return "", fmt.Errorf("upload source: %w", err)
	}
	return overlay.Digest.String(), nil
}
