package installation

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
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
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/holon-sh/cli/pkg/recipe"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/holon-sh/protocol"
	"github.com/holon-sh/protocol/runtimecontract"
)

type packedSource struct {
	overlay ocispec.Descriptor
	file    string
	upload  *imageUpload
}

func (d *devLoop) served(ctx context.Context) (map[string]*typegen.Served, error) {
	served := map[string]*typegen.Served{}
	for _, app := range d.manifest.Apps {
		sent, running := d.sent[app.Name]
		if !running {
			continue
		}
		request := protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: d.session.Name}, App: app.Name}
		answer := &typegen.Served{}
		if err := d.manager.Call(ctx, d.options.plane, protocol.ToolGetAppContract, request, answer); err == nil {
			served[app.Name] = answer
		} else if sent != "" {
			return nil, fmt.Errorf("%s: %w", app.Name, err)
		}
	}
	return served, nil
}

var (
	errUnstarted        = errors.New("an app did not start from its source")
	errSourceSuperseded = errors.New("superseded by a newer save")
	errSourceUnrecorded = errors.New("no answer within a minute")
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
	if !d.reload(ctx, slices.DeleteFunc(slices.Clone(d.manifest.Apps), func(app manifestApp) bool {
		_, running := d.sent[app.Name]
		return running || len(app.resolved.Requires) != 0
	})) {
		return errUnstarted
	}
	for {
		last := !slices.ContainsFunc(d.manifest.Apps, d.unsent)
		served, err := d.served(ctx)
		if err != nil {
			return err
		}
		manifest := d.manifest
		manifest.Apps = slices.DeleteFunc(slices.Clone(manifest.Apps), d.waiting)
		prepared, cleanup, err := prepareManifestTypes(ctx, manifest, d.options, served)
		if err != nil {
			seeded := len(d.sent)
			maps.DeleteFunc(d.sent, func(_, sent string) bool { return sent == "" })
			if len(d.sent) != seeded {
				return d.generate(ctx)
			}
			return err
		}
		cleanup()
		for _, app := range prepared.Apps {
			if p := app.resolved.types; p != nil {
				d.accepted[app.Name] = p
			}
		}
		if !d.reload(ctx, manifest.Apps) {
			return errUnstarted
		}
		if last {
			return nil
		}
	}
}

func (d *devLoop) reload(ctx context.Context, apps []manifestApp) bool {
	reloaded := true
	for _, app := range apps {
		if app.Path == "" {
			continue
		}
		if err := d.reloadApp(ctx, app); err != nil {
			reason := err.Error()
			if failure, ok := errors.AsType[*httpError](err); ok && failure.Message != "" {
				reason = failure.Message
				if d.options.output.verbose {
					_, _ = io.WriteString(d.options.output.writer, failure.Output)
				}
			}
			d.fail(app.Name, reason)
			reloaded = false
		}
	}
	return reloaded
}

func (d *devLoop) unsent(app manifestApp) bool {
	return app.Path != "" && d.sent[app.Name] == ""
}

func (d *devLoop) waiting(app manifestApp) bool {
	return slices.ContainsFunc(d.manifest.Apps, func(required manifestApp) bool {
		return d.unsent(required) && slices.Contains(app.resolved.Requires, required.Name)
	})
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
	packed, err := packSource(ctx, app, root, d.accepted[app.Name])
	packed.upload = newImageUpload(d.options, manifestSpec{Apps: []manifestApp{app}})
	return packed, cleanup, err
}

func (d *devLoop) reloadApp(ctx context.Context, app manifestApp) error {
	defer d.options.output.measure(app.Name, "reload")()
	if !slices.ContainsFunc(d.session.Apps, func(current protocol.App) bool { return current.Name == app.Name }) {
		return errors.New("application removed from the session")
	}
	packed, cleanup, err := d.pack(ctx, app)
	if err == nil && packed.overlay.Digest.String() != d.sent[app.Name] {
		d.begin(app.Name)
		if err = d.send(ctx, app.Name, packed); err == nil {
			d.record(app.Name, packed, cleanup)
			return nil
		}
		delete(d.sent, app.Name)
	}
	cleanup()
	return err
}

func (d *devLoop) send(ctx context.Context, app string, packed packedSource) error {
	var result protocol.ReloadAppResult
	if err := d.manager.postOverlay(ctx, d.session.Name, app, "source", packed.file, &result); err != nil {
		return err
	}
	d.sent[app] = packed.overlay.Digest.String()
	if d.options.output.verbose {
		_, _ = io.WriteString(d.options.output.writer, result.Output)
	}
	return nil
}

func (d *devLoop) record(app string, packed packedSource, cleanup func()) {
	if cancel := d.recording[app]; cancel != nil {
		cancel(errSourceSuperseded)
	}
	ctx, cancel := context.WithCancelCause(d.background)
	d.recording[app] = cancel
	d.clock.AfterFunc(time.Minute, func() { cancel(errSourceUnrecorded) })
	session := d.session.UID
	d.records.Go(func() {
		defer cleanup()
		uploaded := d.options.output.measure(app, "source upload")
		err := packed.publish(ctx, app, session)
		uploaded()
		if cause := cmp.Or(context.Cause(ctx), err); err != nil && cause != errSourceSuperseded {
			d.options.output.unrecorded(app, cause)
		}
	})
}

func packSource(ctx context.Context, app manifestApp, root string, accepted *preparedTypes) (packedSource, error) {
	if accepted != nil {
		staged := *accepted
		staged.root = root
		if err := staged.prepared.WriteClients(staged.name, staged.root); err != nil {
			return packedSource{}, err
		}
	}
	start, err := sourceAppStart(app, root)
	if err != nil {
		return packedSource{}, err
	}
	// A page is built once the clients it may import are in the source.
	plan, err := overlayPlan(root, start, accepted != nil && !accepted.unserved)
	if err != nil {
		return packedSource{}, fmt.Errorf("%s: %w", app.Name, err)
	}
	if err := writeHolonFile(root, runtimecontract.PlanFile, plan); err != nil {
		return packedSource{}, err
	}
	var packed packedSource
	packed.overlay, packed.file, err = packOverlay(ctx, root)
	return packed, err
}

func writeHolonFile(root, name string, value any) error {
	data, _ := json.Marshal(value)
	return writeSourceFile(root, name, data)
}

func writeSourceFile(root, name string, data []byte) error {
	target := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o644)
}

func packOverlay(ctx context.Context, root string) (ocispec.Descriptor, string, error) {
	source, err := fsutil.NewFS(root)
	if err != nil {
		return ocispec.Descriptor{}, "", err
	}
	stable, err := fsutil.NewFilterFS(source, &fsutil.FilterOpt{Map: stableStat})
	if err != nil {
		return ocispec.Descriptor{}, "", err
	}
	name := root + ".tar.zst"
	file, err := os.Create(name)
	if err != nil {
		return ocispec.Descriptor{}, "", err
	}
	defer func() { _ = file.Close() }()
	digester := digest.SHA256.Digester()
	compressed, err := zstd.NewWriter(io.MultiWriter(file, digester.Hash()), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return ocispec.Descriptor{}, "", err
	}
	if err := errors.Join(fsutil.WriteTar(ctx, stable, compressed), compressed.Close()); err != nil {
		return ocispec.Descriptor{}, "", err
	}
	overlay := ocispec.Descriptor{MediaType: runtimecontract.OverlayMediaType, Digest: digester.Digest()}
	overlay.Size, err = file.Seek(0, io.SeekCurrent)
	return overlay, name, err
}

func (m *Client) postOverlay(ctx context.Context, session, app, route, overlay string, result any) error {
	file, err := os.Open(overlay)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	token, err := m.controlToken(ctx)
	if err != nil {
		return err
	}
	request := apiRequest{Plane: protocol.DevPlane, Token: token, Body: file, ContentType: runtimecontract.OverlayMediaType}
	return m.client.send(ctx, path.Join(protocol.APIPathSessions, session, "apps", app, route), request, result)
}

func overlayPlan(root string, start []string, page bool) (runtimecontract.Plan, error) {
	source := os.DirFS(root)
	steps, err := recipe.Read(source)
	if err != nil {
		return runtimecontract.Plan{}, err
	}
	inputs := digest.SHA256.Digester()
	for _, name := range steps.Inputs {
		data, err := fs.ReadFile(source, name)
		if err != nil {
			return runtimecontract.Plan{}, err
		}
		_, _ = fmt.Fprintf(inputs.Hash(), "%s\x00%d\x00%s", name, len(data), data)
	}
	plan := runtimecontract.Plan{Install: runtimecontract.Install{Inputs: inputs.Digest().String(), Commands: steps.Install}, Start: start}
	if page {
		plan.Prepare = steps.Build
	}
	return plan, nil
}

func (p packedSource) publish(ctx context.Context, app, session string) error {
	file, err := os.Open(p.file)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return p.upload.repository(ctx, app, func(repository *remote.Repository, _ protocol.UploadGrant) error {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := repository.Blobs().Push(ctx, p.overlay, file); err != nil {
			return err
		}
		manifest, err := oras.PackManifest(ctx, repository, oras.PackManifestVersion1_1, runtimecontract.OverlayMediaType,
			oras.PackManifestOptions{Layers: []ocispec.Descriptor{p.overlay}})
		if err != nil {
			return err
		}
		return repository.Tag(ctx, manifest, session)
	})
}

func stableStat(_ string, s *fstypes.Stat) fsutil.MapResult {
	s.ModTime, s.Uid, s.Gid, s.Xattrs = 0, 0, 0, nil
	return fsutil.MapResultKeep
}
