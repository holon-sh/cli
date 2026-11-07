package installation

import (
	"archive/tar"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/holon-sh/protocol"
)

// These belong in the protocol module once the source overlay leaves v0.
const (
	toolReloadApp    = "reload_app"
	overlayMediaType = "application/vnd.holon.source-overlay.v0.tar+zstd"
)

type reloadAppRequest struct {
	protocol.AppRequest

	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
}

type reloadAppResult struct {
	RestartedAt    time.Time `json:"restartedAt"`
	ReadyAt        time.Time `json:"readyAt"`
	Digest         string    `json:"digest"`
	ElapsedSeconds float64   `json:"elapsedSeconds"` // how long the API waited, from fetching the source to the app being ready
}

// imageBuildInput reports whether a file, relative to its app, is consumed by the image's build steps,
// so that changing it needs a rebuild rather than a source overlay. This is deliberately a plain list
// for v0; it will be refined, for example from the steps the build plan actually runs.
func imageBuildInput(relative string) bool {
	relative = filepath.ToSlash(relative)
	if first, _, _ := strings.Cut(relative, "/"); first == "ui" {
		return true
	}
	name := path.Base(relative)
	if strings.HasPrefix(name, "vite.config.") || strings.HasPrefix(name, "tsconfig.") {
		return true
	}
	return slices.Contains([]string{
		manifestFileName, "holon.yml", "server.json", "railpack.json", "Dockerfile", dockerignoreFile, "MANIFEST.in",
		"pyproject.toml", "requirements.txt", "uv.lock", "poetry.lock", "Pipfile", "Pipfile.lock",
		"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb",
		"go.mod", "go.sum",
	}, name)
}

// reloadable reports whether a change can restart its apps in place: only source edits, to apps that
// run under the Holon launcher and are already running the last accepted build.
func (d *devLoop) reloadable(change devChange) bool {
	if change.manifest || change.imageInputs || d.pending.Len() != 0 || d.changedImages.Len() != 0 {
		return false
	}
	apps := d.changedApps(change)
	return len(apps) != 0 && !slices.ContainsFunc(apps, func(app manifestApp) bool {
		entry, err := sourceEntry(app.Path)
		return err != nil || app.resolved.stdio || !hostedEntry(entry) || d.options.types.accepted[app.Name] == nil
	})
}

func (d *devLoop) changedApps(change devChange) []manifestApp {
	return slices.DeleteFunc(slices.Clone(d.manifest.Apps), func(app manifestApp) bool {
		return app.Path == "" || !change.contexts.Has(app.Path)
	})
}

// reload restarts each changed app in place and returns the build contexts of those it could not
// reload. Contract extraction and client generation run alongside, so that typed clients still update
// without delaying the restart.
func (d *devLoop) reload(ctx context.Context, change devChange) sets.Set[string] {
	var extraction errgroup.Group
	var prepared manifestSpec
	extraction.Go(func() error {
		result, cleanup, err := prepareManifestTypes(ctx, d.manifest, d.options)
		if err != nil {
			return err
		}
		cleanup()
		prepared = result
		return nil
	})
	failed := sets.New[string]()
	apps := d.changedApps(change)
	for _, app := range apps {
		d.options.output.status(app.Name, "reloading", progressWorking)
		if err := d.reloadApp(ctx, app); err != nil {
			d.options.output.status(app.Name, fmt.Sprintf("reload failed: %v; rebuilding", err), progressWorking)
			failed.Insert(app.Path)
		}
	}
	err := extraction.Wait()
	if failed.Len() != 0 || ctx.Err() != nil {
		// The rebuild prepares again and reports its own failure.
		return failed
	}
	d.options.output.problem("MCP types", err)
	if err != nil {
		return failed
	}
	accepted := prepared
	accepted.Apps = slices.DeleteFunc(slices.Clone(prepared.Apps), func(app manifestApp) bool { return !change.contexts.Has(app.Path) })
	d.options.types.accept(accepted)
	// A new contract can leave another app's clients stale, and those are compiled into its image.
	if slices.ContainsFunc(prepared.Apps, func(app manifestApp) bool { return d.options.types.affected(ctx, app, prepared) }) {
		if err := d.rebuild(ctx, devChange{contexts: sets.New[string]()}); err != nil && ctx.Err() == nil {
			d.options.output.problem("app rebuild", fmt.Errorf("%w; existing service remains available", err))
		}
	}
	return failed
}

func (d *devLoop) reloadApp(ctx context.Context, app manifestApp) error {
	defer d.options.output.measure(app.Name, "reload")()
	directory, err := os.MkdirTemp("", "holon-overlay-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	archive := filepath.Join(directory, "overlay.tar.zst")
	overlay, err := d.buildOverlay(app, filepath.Join(directory, "source"), archive)
	if err != nil {
		return err
	}
	if overlay.Digest.String() == d.reloaded[app.Name] {
		d.options.output.status(app.Name, "unchanged", progressDone)
		return nil
	}
	upload := newImageUpload(d.manager, d.name, manifestSpec{Apps: []manifestApp{app}})
	if err := pushOverlay(ctx, upload, app.Name, overlay, archive, d.options); err != nil {
		return fmt.Errorf("upload source: %w", err)
	}
	seconds, err := d.callReload(ctx, app.Name, overlay.Digest.String())
	if err != nil {
		return err
	}
	d.reloaded[app.Name] = overlay.Digest.String()
	d.options.output.status(app.Name, fmt.Sprintf("reloaded in %.1f s", seconds), progressDone)
	return nil
}

// callReload restarts app from the overlay blob digest names and reports how long the restart took.
func (d *devLoop) callReload(ctx context.Context, app, digest string) (float64, error) {
	var result reloadAppResult
	request := reloadAppRequest{
		AppRequest: protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: d.name}, App: app},
		Digest:     digest, MediaType: overlayMediaType,
	}
	if err := d.manager.Call(ctx, ControlPlane(d.name), toolReloadApp, request, &result); err != nil {
		return 0, err
	}
	if result.Digest != request.Digest {
		return 0, errors.New("the app restarted from different source")
	}
	return cmp.Or(result.ElapsedSeconds, result.ReadyAt.Sub(result.RestartedAt).Seconds()), nil
}

// noticeRestart records a restart this loop did not cause: the session going unready and ready again,
// as it does while a restarted pod boots from its image. A renewal moves only the revision. The session
// reports readiness for all its apps together, so a restart of any app re-sends every overlay.
func (d *devLoop) noticeRestart(current protocol.SessionResponse) {
	ready := current.Condition(protocol.ConditionReady)
	if ready.Status != "True" {
		d.unready = d.unready || d.pending.Len() == 0
		return
	}
	since := time.Time(ready.LastTransitionTime)
	if d.pending.Len() == 0 && (d.unready || !d.readySince.IsZero() && since.After(d.readySince)) {
		d.restarted = len(d.reloaded) != 0
	}
	d.readySince, d.unready = since, false
}

const restartRetries = 5

// resend restarts each app that was running an overlay from that overlay again, and rebuilds those it
// cannot, so that none keeps serving its image's older code.
func (d *devLoop) resend(ctx context.Context) {
	failed := sets.New[string]()
	for _, app := range d.manifest.Apps {
		digest := d.reloaded[app.Name]
		if digest == "" {
			continue
		}
		d.options.output.status(app.Name, "restarted, reloading", progressWorking)
		seconds, err := d.callReload(ctx, app.Name, digest)
		// A runtime answers 503 while its app is still starting, after the pod already reports ready.
		for attempt := 0; err != nil && attempt < restartRetries; attempt++ {
			select {
			case <-ctx.Done():
				return
			case <-d.clock.After(time.Second):
			}
			seconds, err = d.callReload(ctx, app.Name, digest)
		}
		if err != nil {
			d.options.output.status(app.Name, fmt.Sprintf("restarted on its image; reload failed: %v; rebuilding", err), progressWorking)
			delete(d.reloaded, app.Name)
			failed.Insert(app.Path)
			continue
		}
		d.options.output.status(app.Name, fmt.Sprintf("restarted, reloaded in %.1f s", seconds), progressDone)
	}
	if failed.Len() == 0 {
		return
	}
	if err := d.rebuild(ctx, devChange{contexts: failed, imageInputs: true}); err != nil && ctx.Err() == nil {
		d.options.output.problem("app rebuild", fmt.Errorf("%w; existing service remains available", err))
	}
}

// buildOverlay fills snapshot as an image build would, with the clients of the last accepted build,
// and archives it.
func (d *devLoop) buildOverlay(app manifestApp, snapshot, archive string) (ocispec.Descriptor, error) {
	if _, err := snapshotSource(app, snapshot); err != nil {
		return ocispec.Descriptor{}, err
	}
	types := *d.options.types.accepted[app.Name]
	types.root = snapshot
	if _, err := stageTypes(&types, false); err != nil {
		return ocispec.Descriptor{}, err
	}
	entry, err := sourceEntry(snapshot)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if _, err := stageLauncher(snapshot, entry); err != nil {
		return ocispec.Descriptor{}, err
	}
	return writeOverlay(snapshot, archive)
}

// writeOverlay archives root with a stable file order and no timestamps or owners, so that an unchanged
// snapshot yields the same digest. It leaves out .holon-types, which only image builds read.
func writeOverlay(root, destination string) (descriptor ocispec.Descriptor, err error) {
	file, err := os.Create(destination)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	digester := digest.SHA256.Digester()
	compressed, err := zstd.NewWriter(io.MultiWriter(file, digester.Hash()), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	archive := tar.NewWriter(compressed)
	err = filepath.WalkDir(root, func(current string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil || relative == "." {
			return err
		}
		if relative == ".holon-types" {
			return filepath.SkipDir
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		return writeOverlayEntry(archive, current, filepath.ToSlash(relative), info)
	})
	if err = errors.Join(err, archive.Close(), compressed.Close()); err != nil {
		return ocispec.Descriptor{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return ocispec.Descriptor{MediaType: overlayMediaType, Digest: digester.Digest(), Size: info.Size()}, nil
}

func writeOverlayEntry(archive *tar.Writer, source, name string, info fs.FileInfo) error {
	header := &tar.Header{Name: name, Mode: int64(info.Mode().Perm()), ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
	switch {
	case info.IsDir():
		header.Typeflag, header.Name = tar.TypeDir, name+"/"
	case info.Mode()&os.ModeSymlink != 0:
		link, err := os.Readlink(source)
		if err != nil {
			return err
		}
		header.Typeflag, header.Linkname = tar.TypeSymlink, link
	default:
		header.Typeflag, header.Size = tar.TypeReg, info.Size()
	}
	if err := archive.WriteHeader(header); err != nil || header.Typeflag != tar.TypeReg {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	_, err = io.Copy(archive, input)
	return err
}

func pushOverlay(ctx context.Context, upload *imageUpload, app string, overlay ocispec.Descriptor, archive string, options buildOptions) error {
	return upload.run(ctx, func(grant protocol.UploadGrant) error {
		repository, err := imageRepository(grant.Registry+"/"+grant.Repositories[app], grant.Grant, options.HTTPClient)
		if err != nil {
			return err
		}
		exists, err := repository.Blobs().Exists(ctx, overlay)
		if err != nil || exists {
			return err
		}
		file, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		return repository.Blobs().Push(ctx, overlay, file)
	})
}
