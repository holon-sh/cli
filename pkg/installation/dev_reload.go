package installation

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

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
	toolRecordAppSource = "record_app_source"
	toolGetAppContract  = "get_app_contract"
	overlayMediaType    = "application/vnd.holon.source-overlay.v0.tar+zstd"
)

type recordAppSourceRequest struct {
	protocol.AppRequest

	Digest string `json:"digest"`
}

type reloadAppResult struct {
	ElapsedSeconds float64 `json:"elapsedSeconds"`
}

type packedSource struct {
	overlay ocispec.Descriptor
	file    string
	request buildRequest
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

var (
	errUnstarted        = errors.New("an app did not start from its source")
	errSourceSuperseded = errors.New("superseded by a newer save")
	errSourceUnrecorded = errors.New("was not recorded in the session within a minute")
)

func (d *devLoop) regenerate(ctx context.Context) bool {
	err := d.generate(ctx)
	if errors.Is(err, errUnstarted) {
		return false
	}
	d.options.output.problem("MCP types", err)
	return true
}

func (d *devLoop) generate(ctx context.Context) error {
	if !d.reload(ctx, d.paths(true)) {
		return errUnstarted
	}
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
	if !d.reload(ctx, d.paths(false)) {
		return errUnstarted
	}
	return nil
}

func (d *devLoop) reload(ctx context.Context, contexts sets.Set[string]) bool {
	reloaded := true
	for _, app := range d.manifest.Apps {
		if !contexts.Has(app.Path) {
			continue
		}
		if err := d.reloadApp(ctx, app); err != nil {
			d.options.output.status(app.Name, fmt.Sprintf("reload failed: %v", err), progressError)
			d.pending.Delete(app.Name)
			reloaded = false
		}
	}
	return reloaded
}

func recordedSources(apps []protocol.App) map[string]string {
	recorded := map[string]string{}
	for _, app := range apps {
		recorded[app.Name] = app.Overlay
	}
	return recorded
}

func (d *devLoop) paths(unsent bool) sets.Set[string] {
	paths := sets.New[string]()
	for _, app := range d.manifest.Apps {
		if app.Path != "" && !(unsent && d.sent[app.Name] != "") {
			paths.Insert(app.Path)
		}
	}
	return paths
}

func (d *devLoop) pack(ctx context.Context, app manifestApp) (packedSource, func(), error) {
	directory, err := os.MkdirTemp("", "holon-overlay-")
	if err != nil {
		return packedSource{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	root := filepath.Join(directory, "source")
	if _, err := snapshotSource(app, root); err != nil {
		return packedSource{}, cleanup, err
	}
	upload := newImageUpload(d.manager, d.name, manifestSpec{Apps: []manifestApp{app}})
	packed, err := d.options.packSource(ctx, app, root, upload)
	return packed, cleanup, err
}

func (d *devLoop) reloadApp(ctx context.Context, app manifestApp) error {
	defer d.options.output.measure(app.Name, "reload")()
	if !slices.ContainsFunc(d.session.Apps, func(current protocol.App) bool { return current.Name == app.Name }) {
		return errors.New("application removed from the session")
	}
	packed, cleanup, err := d.pack(ctx, app)
	if err == nil && packed.overlay.Digest.String() != d.sent[app.Name] {
		if err = d.send(ctx, app.Name, packed); err == nil {
			d.record(app.Name, packed, cleanup)
			return nil
		}
	}
	cleanup()
	return err
}

func (d *devLoop) send(ctx context.Context, app string, packed packedSource) error {
	d.options.output.status(app, "reloading", progressWorking)
	file, err := os.Open(packed.file)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	token, err := d.manager.controlToken(ctx)
	if err != nil {
		return err
	}
	var result reloadAppResult
	request := apiRequest{Plane: ControlPlane(d.name), Token: token, Body: file, ContentType: overlayMediaType}
	if err := d.manager.client.send(ctx, path.Join(protocol.APIPathSessions, d.name, "apps", app, "source"), request, &result); err != nil {
		return err
	}
	d.sent[app] = packed.overlay.Digest.String()
	d.options.output.status(app, fmt.Sprintf("reloaded in %.1f s", result.ElapsedSeconds), progressDone)
	return nil
}

func (d *devLoop) record(app string, packed packedSource, cleanup func()) {
	if cancel := d.recording[app]; cancel != nil {
		cancel(errSourceSuperseded)
	}
	ctx, cancel := context.WithCancelCause(d.background)
	d.recording[app] = cancel
	d.clock.AfterFunc(time.Minute, func() { cancel(errSourceUnrecorded) })
	d.records.Go(func() {
		defer cleanup()
		err := packed.publish(ctx)
		if err == nil {
			request := recordAppSourceRequest{
				AppRequest: protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: d.name}, App: app},
				Digest:     packed.overlay.Digest.String(),
			}
			err = d.manager.Call(ctx, ControlPlane(d.name), toolRecordAppSource, request, nil)
		}
		if err != nil {
			err = cmp.Or(context.Cause(ctx), err)
		}
		if err != errSourceSuperseded {
			d.options.output.problem(app+" source", err)
		}
	})
}

func (o buildOptions) packSource(ctx context.Context, app manifestApp, root string, upload *imageUpload) (packedSource, error) {
	if accepted := o.types.accepted[app.Name]; accepted != nil {
		staged := *accepted
		staged.root = root
		if err := stageTypes(&staged); err != nil {
			return packedSource{}, err
		}
	}
	entry, err := hostedEntry(app, root)
	if err != nil {
		return packedSource{}, err
	}
	if entry == "" {
		return packedSource{}, fmt.Errorf("%s: holon dev runs an app from a server.py, server.ts or server.js entry; holon deploy still builds this app", app.Name)
	}
	source, err := fsutil.NewFS(root)
	if err != nil {
		return packedSource{}, err
	}
	stable, err := fsutil.NewFilterFS(source, &fsutil.FilterOpt{Map: stableStat})
	if err != nil {
		return packedSource{}, err
	}
	packed := packedSource{
		file:    root + ".tar.zst",
		request: buildRequest{app: app, upload: upload, httpClient: o.HTTPClient},
	}
	file, err := os.Create(packed.file)
	if err != nil {
		return packedSource{}, err
	}
	defer func() { _ = file.Close() }()
	digester := digest.SHA256.Digester()
	compressed, err := zstd.NewWriter(io.MultiWriter(file, digester.Hash()), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return packedSource{}, err
	}
	if err := errors.Join(fsutil.WriteTar(ctx, stable, compressed), compressed.Close()); err != nil {
		return packedSource{}, err
	}
	packed.overlay = ocispec.Descriptor{MediaType: overlayMediaType, Digest: digester.Digest()}
	packed.overlay.Size, err = file.Seek(0, io.SeekCurrent)
	return packed, err
}

func (p packedSource) publish(ctx context.Context) error {
	file, err := os.Open(p.file)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	err = p.request.publish(ctx, func(repository *remote.Repository, _ protocol.UploadGrant) error {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return repository.Blobs().Push(ctx, p.overlay, file)
	})
	if err != nil {
		return fmt.Errorf("upload source: %w", err)
	}
	return nil
}

func stableStat(_ string, s *fstypes.Stat) fsutil.MapResult {
	s.ModTime, s.Uid, s.Gid, s.Xattrs = 0, 0, 0, nil
	return fsutil.MapResultKeep
}
