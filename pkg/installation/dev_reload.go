package installation

import (
	"archive/tar"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"oras.land/oras-go/v2/registry/remote"

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

// serverSource reports whether a changed file, relative to its app, is server code that a source overlay
// can deliver: a Python or JavaScript module outside ui/. Any other file, from a lockfile to a data file,
// may be read by the image build, so it takes the rebuild path.
func serverSource(relative string) bool {
	relative = filepath.ToSlash(relative)
	first, _, _ := strings.Cut(relative, "/")
	return first != "ui" && slices.Contains([]string{".py", ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts"}, path.Ext(relative))
}

// imageInputs fingerprints the files of snapshot that are not server source, which an image build may read.
// Two snapshots with the same image inputs differ only in what a source overlay delivers.
func imageInputs(snapshot string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(snapshot, func(current string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, _ := filepath.Rel(snapshot, current)
		if item.IsDir() || serverSource(relative) {
			return nil
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", filepath.ToSlash(relative), info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(current)
			_, _ = io.WriteString(hash, link)
			return err
		}
		file, err := os.Open(current)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		_, err = io.Copy(hash, file)
		return err
	})
	return hex.EncodeToString(hash.Sum(nil)), err
}

// reloadable reports whether a change can restart its apps in place: only source edits, to apps that
// run under the Holon launcher, while no image change is pending.
func (d *devLoop) reloadable(change devChange) bool {
	if change.manifest || change.imageInputs || d.pending.Len() != 0 || d.changedImages.Len() != 0 {
		return false
	}
	apps := d.changedApps(change)
	return len(apps) != 0 && !slices.ContainsFunc(apps, func(app manifestApp) bool {
		p := d.options.types.accepted[app.Name]
		return p == nil || !p.hosted
	})
}

func (d *devLoop) changedApps(change devChange) []manifestApp {
	return slices.DeleteFunc(slices.Clone(d.manifest.Apps), func(app manifestApp) bool {
		return app.Path == "" || !change.contexts.Has(app.Path)
	})
}

// reload restarts each changed app in place and returns the build contexts of those it could not
// reload, and whether a new contract left another app's compiled clients stale. Contract extraction and
// client generation run alongside, so that typed clients still update without delaying the restart.
func (d *devLoop) reload(ctx context.Context, change devChange) (sets.Set[string], bool) {
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
		return failed, false
	}
	d.options.output.problem("MCP types", err)
	if err != nil {
		return failed, false
	}
	accepted := prepared
	accepted.Apps = slices.DeleteFunc(slices.Clone(prepared.Apps), func(app manifestApp) bool { return !change.contexts.Has(app.Path) })
	d.options.types.accept(accepted)
	return failed, slices.ContainsFunc(prepared.Apps, func(app manifestApp) bool { return d.options.types.affected(ctx, app, prepared) })
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
	if err := pushOverlay(ctx, upload, app, overlay, archive, d.options); err != nil {
		return fmt.Errorf("upload source: %w", err)
	}
	var result reloadAppResult
	request := reloadAppRequest{
		AppRequest: protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: d.name}, App: app.Name},
		Digest:     overlay.Digest.String(), MediaType: overlayMediaType,
	}
	if err := d.manager.Call(ctx, ControlPlane(d.name), toolReloadApp, request, &result); err != nil {
		return err
	}
	if result.Digest != request.Digest {
		return errors.New("the app restarted from different source")
	}
	d.reloaded[app.Name] = request.Digest
	seconds := cmp.Or(result.ElapsedSeconds, result.ReadyAt.Sub(result.RestartedAt).Seconds())
	d.options.output.status(app.Name, fmt.Sprintf("reloaded in %.1f s", seconds), progressDone)
	return nil
}

// buildOverlay fills snapshot as an image build would, with the clients of the last accepted build,
// and archives it.
func (d *devLoop) buildOverlay(app manifestApp, snapshot, archive string) (ocispec.Descriptor, error) {
	if _, err := snapshotSource(app, snapshot); err != nil {
		return ocispec.Descriptor{}, err
	}
	types := *d.options.types.accepted[app.Name]
	types.root = snapshot
	_, start, err := stageSnapshot(app, &types, false)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if start == nil {
		return ocispec.Descriptor{}, errors.New("the app no longer has a server entry the launcher runs")
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

func pushOverlay(ctx context.Context, upload *imageUpload, app manifestApp, overlay ocispec.Descriptor, archive string, options buildOptions) error {
	return buildRequest{app: app, upload: upload, httpClient: options.HTTPClient}.publish(ctx, func(repository *remote.Repository, _ protocol.UploadGrant) error {
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
