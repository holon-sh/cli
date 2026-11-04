package installation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/holon-sh/cli/pkg/typegen"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"

	"github.com/holon-sh/protocol"
)

const devDebounce = 250 * time.Millisecond

type devChange struct {
	contexts sets.Set[string]
	manifest bool
}

type devWatcher struct {
	watcher  *fsnotify.Watcher
	metadata sets.Set[string]
	contexts sets.Set[string]
	changes  chan devChange
	output   *progressOutput
	done     chan struct{}
	clock    clock.WithTicker
}

func newDevWatcher(manifest manifestSpec, clk clock.WithTicker, output *progressOutput) (*devWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	result := &devWatcher{
		watcher: watcher, metadata: sets.New(manifest.file), contexts: sets.New[string](),
		changes: make(chan devChange), output: output, done: make(chan struct{}), clock: clk,
	}
	for _, app := range manifest.Apps {
		if app.Path == "" {
			continue
		}
		result.metadata.Insert(filepath.Join(app.Path, "server.json"))
		result.contexts.Insert(app.Path)
		if err := result.refresh(app.Path); err != nil {
			_ = watcher.Close()
			return nil, err
		}
	}
	for path := range result.metadata {
		if err := watcher.Add(filepath.Dir(path)); err != nil {
			_ = watcher.Close()
			return nil, fmt.Errorf("watch descriptor or manifest: %w", err)
		}
	}

	go result.run()
	return result, nil
}

func (w *devWatcher) refresh(root string) error {
	matcher, err := typegen.SourceMatcher(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !item.IsDir() {
			return nil
		}
		ignored, matchErr := typegen.IgnoredSource(root, path, matcher)
		if matchErr != nil {
			return matchErr
		}
		if typegen.GeneratedSource(root, path) || ignored && !matcher.Exclusions() {
			return filepath.SkipDir
		}
		return w.watcher.Add(path)
	})
}

func (w *devWatcher) observe(root, path string, op fsnotify.Op) (bool, error) {
	matcher, err := typegen.SourceMatcher(root)
	if err != nil {
		return false, err
	}
	ignored, err := typegen.IgnoredSource(root, path, matcher)
	if err != nil {
		return false, err
	}
	if info, statErr := os.Stat(path); filepath.Base(path) == dockerignoreFile || op.Has(fsnotify.Create) && statErr == nil && info.IsDir() {
		err = w.refresh(root)
	}
	return !ignored, err
}

func (w *devWatcher) run() {
	defer close(w.done)
	pending := devChange{contexts: sets.New[string]()}
	var debounce <-chan time.Time
	var output chan<- devChange
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if !event.Has(fsnotify.Write | fsnotify.Create | fsnotify.Remove | fsnotify.Rename) {
				continue
			}
			path := filepath.Clean(event.Name)
			if w.metadata.Has(path) {
				pending.manifest = true
			}
			for root := range w.contexts {
				if relative, _ := filepath.Rel(root, path); !filepath.IsLocal(relative) {
					continue
				}
				changed, err := w.observe(root, path, event.Op)
				if changed {
					pending.contexts.Insert(root)
				}
				if err != nil {
					w.output.status("file watcher", fmt.Sprintf("continuing after error: %v", err), progressError)
				}
			}
			debounce = w.clock.After(devDebounce)
		case <-debounce:
			if pending.contexts.Len() != 0 || pending.manifest {
				output = w.changes
			}
		case output <- pending:
			pending = devChange{contexts: sets.New[string]()}
			output = nil
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.output.status("file watcher", fmt.Sprintf("continuing after error: %v", err), progressError)
		}
	}
}

func (w *devWatcher) Close() error {
	err := w.watcher.Close()
	<-w.done
	return err
}

func sameManifestTopology(left, right manifestSpec) bool {
	return slices.EqualFunc(left.Apps, right.Apps, func(app, candidate manifestApp) bool {
		left, right := app.resolved.App, candidate.resolved.App
		if (left.Image == "") != (right.Image == "") {
			return false
		}
		left.Image, right.Image = "", ""
		return app.Path == candidate.Path &&
			app.resolved.stdio == candidate.resolved.stdio && reflect.DeepEqual(left, right)
	})
}

func patchSessionImages(
	ctx context.Context,
	manager *Client,
	name string,
	apps []protocol.App,
) (protocol.SessionResponse, error) {
	current, err := manager.Session(ctx, name)
	if err != nil {
		return protocol.SessionResponse{}, err
	}
	desired := desiredFor(current)
	for _, app := range apps {
		index := slices.IndexFunc(desired.Apps, func(old protocol.App) bool { return old.Name == app.Name })
		if index < 0 {
			return protocol.SessionResponse{}, errors.New("application removed during rebuild")
		}
		desired.Apps[index].Image = app.Image
	}
	return replaceExecution(ctx, manager, name, current, desired, true)
}

type devLoop struct {
	manager                *Client
	name                   string
	manifest               manifestSpec
	apps                   []string
	clock                  clock.WithTicker
	options                buildOptions
	session                protocol.SessionResponse
	changedImages, pending sets.Set[string]
}

func (d *devLoop) renew(ctx context.Context) {
	ticker := d.clock.NewTicker(renewalInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			err := d.manager.Call(ctx, protocol.DevPlane, protocol.ToolRenewSession, protocol.SessionRequest{Session: d.name}, nil)
			if ctx.Err() == nil {
				d.options.output.retry("session renewal", err)
			}
		}
	}
}

func (d *devLoop) run(ctx context.Context, stdout io.Writer) error {
	watcher, err := newDevWatcher(d.manifest, d.clock, d.options.output)
	if err != nil {
		return fmt.Errorf("watch build contexts: %w", err)
	}
	defer func() { _ = watcher.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	var background sync.WaitGroup
	background.Go(func() { d.renew(ctx) })
	background.Go(func() {
		input := protocol.SessionLogsRequest{
			AppRequest: protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: d.name}}, Event: protocol.MCPCallEvent,
		}
		if err := d.manager.StreamLogs(ctx, input, stdout); ctx.Err() == nil {
			d.options.output.status("request stream", "stopped: "+err.Error(), progressError)
		}
	})
	defer func() {
		cancel()
		background.Wait()
	}()
	pollTicker := d.clock.NewTicker(workflowPoll)
	defer pollTicker.Stop()
	d.options.architectures = d.session.BuildArchitectures
	d.changedImages, d.pending = sets.New[string](), sets.New[string]()
	for _, app := range d.session.Apps {
		d.pending.Insert(app.Name)
	}
	announce := sync.OnceValue(func() error {
		if err := printEndpoints(stdout, d.session); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "Watching for changes. Ctrl-C stops this session.")
		return err
	})
	for ctx.Err() == nil {
		if err := d.manager.authorizeOperation(ctx, d.session); err != nil {
			return err
		}
		if err := d.settle(announce); err != nil {
			return err
		}
		phase, state := "Watching for changes", progressIdle
		if d.pending.Len() > 0 {
			phase, state = "Deploying apps", progressWorking
		}
		reportSessionProgress(d.session, d.options.output, phase, state)
		changes := watcher.changes
		if lifecyclePending(d.session) {
			changes = nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-pollTicker.C():
			if err := d.poll(ctx); err != nil {
				return err
			}
		case change := <-changes:
			if err := d.rebuild(ctx, change); err != nil && ctx.Err() == nil {
				d.options.output.status("app rebuild", fmt.Sprintf("%v; existing service remains available", err), progressError)
			}
		}
	}
	return nil
}

func (d *devLoop) settle(announce func() error) error {
	if d.pending.Len() == 0 {
		return nil
	}
	ready, err := SessionReady(d.session)
	if !ready {
		return err
	}
	d.finishPending("ready", progressDone)
	return announce()
}

func (d *devLoop) poll(ctx context.Context) error {
	d.options.output.retry("editor imports", d.options.types.prepared.Write())
	if expiry, err := time.Parse(time.RFC3339Nano, d.session.ExpiresAt); err == nil && !expiry.After(d.clock.Now()) {
		return errors.New("development session lease expired")
	}
	current, err := d.manager.Session(ctx, d.name)
	if slices.Contains([]int{http.StatusNotFound, http.StatusGone}, HTTPStatus(err)) {
		return errors.New("development session is no longer available")
	}
	d.options.output.retry("session status", err)
	if err != nil {
		return nil
	}
	d.session = current
	condition := current.Condition(protocol.ConditionReady)
	if slices.Contains([]string{protocol.ReasonLeaseExpired, protocol.ReasonDeleting}, condition.Reason) {
		return errors.New("development session is no longer active")
	}
	if condition.Status == "True" {
		d.options.output.status("remote deployment", fmt.Sprintf("ready (generation %d)", current.Generation), progressDone)
	}
	if !terminalCondition(condition) {
		return nil
	}
	message := "remote session unavailable"
	if current.Condition(protocol.ConditionAvailable).Status == "True" {
		message = "remote update failed; existing service remains available"
	}
	d.options.output.status("remote deployment",
		fmt.Sprintf("%s (generation %d): %s: %s", message, current.Generation, condition.Reason, condition.Message), progressError)
	d.finishPending("deployment failed", progressError)
	return nil
}

func (d *devLoop) finishPending(phase string, state progressState) {
	for app := range d.pending {
		d.options.output.status(app, phase, state)
	}
	d.pending.Clear()
}

func (d *devLoop) rebuild(ctx context.Context, change devChange) error {
	if change.manifest {
		reloaded, err := resolveManifestFile(d.manifest.file)
		if err == nil {
			reloaded, err = resolveManifestGraph(ctx, reloaded, d.apps...)
		}
		if err != nil {
			return fmt.Errorf("metadata resolution failed: %w", err)
		}
		if !d.updateManifest(reloaded, &change) {
			return nil
		}
	}
	if _, err := d.manager.controlToken(ctx, protocol.DevPlane); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	prepared, cleanup, err := prepareManifestTypes(ctx, d.manifest, d.options)
	if err != nil {
		return fmt.Errorf("remote image preparation failed: %w", err)
	}
	dirty := prepared
	dirty.Apps = slices.DeleteFunc(slices.Clone(prepared.Apps), func(app manifestApp) bool {
		if app.Path == "" {
			return !d.changedImages.Has(app.Name)
		}
		return !d.options.types.affected(ctx, app, prepared) && !change.contexts.Has(app.Path)
	})
	d.options.output.status("", "Preparing apps", progressWorking)
	apps, err := buildManifestImages(ctx, d.manager, d.name, dirty, d.options)
	if err == nil {
		err = currentTypeSources(prepared)
	}
	cleanup()
	if err != nil {
		return fmt.Errorf("remote image preparation failed: %w", err)
	}
	apps = slices.DeleteFunc(apps, func(app protocol.App) bool {
		if !slices.ContainsFunc(d.session.Apps, func(current protocol.App) bool {
			return current.Name == app.Name && current.Image == app.Image
		}) {
			return false
		}
		d.changedImages.Delete(app.Name)
		if d.pending.Has(app.Name) {
			d.options.output.status(app.Name, "deploying", progressWorking)
		} else {
			d.options.output.status(app.Name, "unchanged", progressDone)
		}
		return true
	})
	if len(apps) == 0 {
		d.options.output.status("app rebuild", "remote image is unchanged; continuing to watch", progressDone)
		return nil
	}
	d.options.output.deploying(apps)
	updated, err := patchSessionImages(ctx, d.manager, d.name, apps)
	if err != nil {
		d.options.output.apps(apps, "deployment failed", progressError)
		return fmt.Errorf("remote update failed: %w", err)
	}
	for _, app := range apps {
		d.pending.Insert(app.Name)
		d.changedImages.Delete(app.Name)
	}
	d.options.types.accept(dirty)
	d.session = updated
	d.options.output.status("app rebuild", fmt.Sprintf("remote update requested (generation %d)", updated.Generation), progressDone)
	return nil
}

func (d *devLoop) updateManifest(reloaded manifestSpec, change *devChange) bool {
	if !sameManifestTopology(d.manifest, reloaded) {
		d.options.output.status("app metadata",
			"app topology, dependencies, configuration, egress or access rules changed; restart holon dev to apply it", progressBlocked)
		return false
	}
	d.options.output.status("app metadata", "ok", progressDone)
	for i, app := range reloaded.Apps {
		previous := d.manifest.Apps[i]
		if app.Path != "" {
			change.contexts.Insert(app.Path)
		} else if app.resolved.Image != previous.resolved.Image || !reflect.DeepEqual(app.Registry, previous.Registry) {
			d.changedImages.Insert(app.Name)
		}
	}
	d.manifest = reloaded
	return true
}
