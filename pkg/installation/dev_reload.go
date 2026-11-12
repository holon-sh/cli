package installation

import (
	"context"
	"encoding/json"
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

	"github.com/holon-sh/cli/internal/kiln"
	"github.com/holon-sh/protocol"
)

const (
	toolReloadApp      = "reload_app"
	toolGetAppContract = "get_app_contract"
	overlayMediaType   = "application/vnd.holon.source-overlay.v0.tar+zstd"
)

type reloadAppRequest struct {
	protocol.AppRequest

	Digest string `json:"digest"`
}

type reloadAppResult struct {
	ElapsedSeconds float64 `json:"elapsedSeconds"`
}

func (d *devLoop) served(ctx context.Context) (map[string]json.RawMessage, error) {
	var served map[string]json.RawMessage
	for _, app := range d.manifest.Apps {
		if app.Path == "" {
			continue
		}
		if !slices.ContainsFunc(d.session.Endpoints, func(endpoint protocol.SessionEndpoint) bool { return endpoint.Name == app.Name }) {
			return nil, nil
		}
		request := protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: d.name}, App: app.Name}
		var contract json.RawMessage
		if err := d.manager.Call(ctx, ControlPlane(d.name), toolGetAppContract, request, &contract); err != nil {
			return nil, fmt.Errorf("%s: %w", app.Name, err)
		}
		if served == nil {
			served = map[string]json.RawMessage{}
		}
		served[app.Name] = contract
	}
	return served, nil
}

func (d *devLoop) generate(ctx context.Context) error {
	options := d.options
	var err error
	if options.served, err = d.served(ctx); options.served == nil {
		return err
	}
	prepared, cleanup, err := prepareManifestTypes(ctx, d.manifest, options)
	if err != nil {
		return err
	}
	cleanup()
	d.options.types.accept(prepared)
	for _, app := range d.manifest.Apps {
		if app.Path == "" {
			continue
		}
		if err := d.reloadApp(ctx, app); err != nil {
			return fmt.Errorf("%s: %w", app.Name, err)
		}
	}
	return nil
}

func (d *devLoop) reload(ctx context.Context, contexts sets.Set[string]) {
	reloaded := contexts.Len() != 0
	for _, app := range d.manifest.Apps {
		if !contexts.Has(app.Path) {
			continue
		}
		if err := d.reloadApp(ctx, app); err != nil {
			d.options.output.status(app.Name, fmt.Sprintf("reload failed: %v", err), progressError)
			reloaded = false
		}
	}
	if reloaded && d.pending.Len() == 0 {
		d.options.output.problem("MCP types", d.generate(ctx))
	}
}

func (d *devLoop) reloadApp(ctx context.Context, app manifestApp) error {
	defer d.options.output.measure(app.Name, "reload")()
	i := slices.IndexFunc(d.session.Apps, func(current protocol.App) bool { return current.Name == app.Name })
	if i < 0 {
		return errors.New("application removed from the session")
	}
	directory, err := os.MkdirTemp("", "holon-overlay-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	root := filepath.Join(directory, "source")
	if _, err := snapshotSource(app, root); err != nil {
		return err
	}
	running := &d.session.Apps[i]
	upload := newImageUpload(d.manager, d.name, manifestSpec{Apps: []manifestApp{app}})
	overlay, err := d.options.pushSource(ctx, app, root, upload, running.Overlay)
	if err != nil {
		return err
	}
	if overlay != running.Overlay {
		return d.restart(ctx, running, overlay)
	}
	return nil
}

func (o buildOptions) pushSource(ctx context.Context, app manifestApp, root string, upload *imageUpload, unless string) (string, error) {
	if accepted := o.types.accepted[app.Name]; accepted != nil {
		staged := *accepted
		staged.root = root
		if err := stageTypes(&staged); err != nil {
			return "", err
		}
	}
	entry, err := hostedEntry(app, root)
	if err != nil {
		return "", err
	}
	if entry == "" {
		return "", fmt.Errorf("%s: holon dev runs an app from a server.py, server.ts or server.js entry; holon deploy still builds this app", app.Name)
	}
	request := buildRequest{Request: kiln.Request{Source: root}, app: app, upload: upload, httpClient: o.HTTPClient}
	return request.pushOverlay(ctx, unless)
}

func (d *devLoop) restart(ctx context.Context, running *protocol.App, overlay string) error {
	app := running.Name
	d.options.output.status(app, "reloading", progressWorking)
	var result reloadAppResult
	request := reloadAppRequest{
		AppRequest: protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: d.name}, App: app},
		Digest:     overlay,
	}
	if err := d.manager.Call(ctx, ControlPlane(d.name), toolReloadApp, request, &result); err != nil {
		return err
	}
	running.Overlay = overlay
	d.options.output.status(app, fmt.Sprintf("reloaded in %.1f s", result.ElapsedSeconds), progressDone)
	return nil
}

func stableStat(_ string, s *fstypes.Stat) fsutil.MapResult {
	s.ModTime, s.Uid, s.Gid, s.Xattrs = 0, 0, 0, nil
	return fsutil.MapResultKeep
}

func (r buildRequest) pushOverlay(ctx context.Context, unless string) (string, error) {
	source, err := fsutil.NewFS(r.Source)
	if err != nil {
		return "", err
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
