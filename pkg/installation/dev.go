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
	contexts    sets.Set[string]
	manifest    bool
	imageInputs bool // a changed file is not server source, so an image build may read it
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

// editorFiles are what editors write beside a file while saving it: backups, vim's write test and swap
// files, Emacs locks and auto-saves, and JetBrains' temporary copies. They are not the app's.
var editorFiles = []string{"*~", "4913", ".*.sw?", ".#*", "#*#", "*___jb_*"}

func editorFile(name string) bool {
	return slices.ContainsFunc(editorFiles, func(pattern string) bool {
		matched, _ := filepath.Match(pattern, name)
		return matched
	})
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
			if editorFile(filepath.Base(path)) {
				continue
			}
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
					relative, _ := filepath.Rel(root, path)
					pending.imageInputs = pending.imageInputs || !serverSource(relative)
				}
				w.output.problem("file watcher", err)
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
			w.output.problem("file watcher", err)
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
	rebuilt := make([]protocol.App, 0, len(apps))
	for _, app := range apps {
		index := slices.IndexFunc(current.Apps, func(old protocol.App) bool { return old.Name == app.Name })
		if index < 0 {
			return protocol.SessionResponse{}, errors.New("application removed during rebuild")
		}
		updated := current.Apps[index]
		updated.Image = app.Image
		rebuilt = append(rebuilt, updated)
	}
	return replaceExecution(ctx, manager, name, current, protocol.AppsRequestFor[protocol.App]{Apps: protocol.WritableApps(rebuilt)})
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
	reloaded               map[string]string // the overlay digest each app last restarted from
	preparing              chan preparation  // what a reload's preparation produces, until the loop accepts it
	cancelPreparation      context.CancelFunc
}

func (d *devLoop) renew(ctx context.Context) {
	ticker := d.clock.NewTicker(renewalInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			// A failed renewal needs no report: poll ends the session when the lease expires or credentials are lost.
			_ = d.manager.Call(ctx, protocol.DevPlane, protocol.ToolRenewSession, protocol.SessionRequest{Session: d.name}, nil)
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
		if err := d.manager.StreamLogs(ctx, input, d.options.output.logs(stdout)); ctx.Err() == nil {
			d.options.output.problem("request stream", fmt.Errorf("stopped: %w", err))
		}
	})
	defer func() {
		cancel()
		background.Wait()
		d.stopPreparation()
	}()
	d.options.architectures = d.session.BuildArchitectures
	d.changedImages, d.pending, d.reloaded = sets.New[string](), sets.New[string](), map[string]string{}
	for _, app := range d.session.Apps {
		d.pending.Insert(app.Name)
	}
	announce := sync.OnceValue(func() error {
		if err := d.options.output.endpoints(stdout, "Ready", d.session, false); err != nil {
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
		read, stopRead := d.readSession(ctx)
		select {
		case <-ctx.Done():
			stopRead()
			return nil
		case result := <-d.preparing:
			stopRead()
			if err := d.acceptPreparation(ctx, result); err != nil && ctx.Err() == nil {
				d.options.output.problem("app rebuild", fmt.Errorf("%w; existing service remains available", err))
			}
		case result := <-read:
			stopRead()
			if err := d.poll(result.session, result.err); err != nil {
				return err
			}
		case change := <-changes:
			stopRead()
			if err := d.rebuild(ctx, change); err != nil && ctx.Err() == nil {
				d.options.output.problem("app rebuild", fmt.Errorf("%w; existing service remains available", err))
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

type sessionRead struct {
	session protocol.SessionResponse
	err     error
}

// readSession long-polls the session in the background, so that a save can interrupt it. A failed read
// answers at once, so it is reported only after workflowPoll, which paces reads through an outage.
func (d *devLoop) readSession(ctx context.Context) (<-chan sessionRead, context.CancelFunc) {
	ctx, stop := context.WithCancel(ctx)
	revision := d.session.Revision
	result := make(chan sessionRead, 1)
	go func() {
		session, err := d.manager.awaitSession(ctx, d.name, revision)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-d.clock.After(workflowPoll):
			}
		}
		result <- sessionRead{session, err}
	}()
	return result, stop
}

func (d *devLoop) poll(current protocol.SessionResponse, err error) error {
	d.options.output.problem("editor imports", d.options.types.repair())
	if expiry, err := time.Parse(time.RFC3339Nano, d.session.ExpiresAt); err == nil && !expiry.After(d.clock.Now()) {
		return errors.New("development session lease expired")
	}
	if slices.Contains([]int{http.StatusNotFound, http.StatusGone}, HTTPStatus(err)) {
		return errors.New("development session is no longer available")
	}
	if credentialsLost(err) {
		return err
	}
	if err != nil {
		return nil
	}
	d.session = current
	condition := current.Condition(protocol.ConditionReady)
	if slices.Contains([]string{protocol.ReasonLeaseExpired, protocol.ReasonDeleting}, condition.Reason) {
		return errors.New("development session is no longer active")
	}
	if condition.Status == "True" {
		d.options.output.problem("remote deployment", nil)
	}
	if !terminalCondition(condition) {
		return nil
	}
	message := "remote session unavailable"
	if current.Condition(protocol.ConditionAvailable).Status == "True" {
		message = "remote update failed; existing service remains available"
	}
	d.options.output.problem("remote deployment",
		fmt.Errorf("%s (generation %d): %s: %s", message, current.Generation, condition.Reason, condition.Message))
	d.finishPending("deployment failed", progressError)
	return nil
}

func (d *devLoop) finishPending(phase string, state progressState) {
	for app := range d.pending {
		d.options.output.status(app, phase, state)
	}
	d.pending.Clear()
}

// rebuild applies a saved change: in place when its apps can reload, and through new images otherwise.
func (d *devLoop) rebuild(ctx context.Context, change devChange) error {
	if d.reloadable(change) {
		failed := d.reload(ctx, change)
		if failed.Len() == 0 {
			return nil
		}
		change.contexts = failed
	}
	return d.buildImages(ctx, change)
}

func (d *devLoop) buildImages(ctx context.Context, change devChange) error {
	if change.manifest {
		reloaded, err := resolveManifestFile(d.manifest.file)
		if err == nil {
			reloaded.sources, reloaded.access = d.manifest.sources, d.manifest.access
			reloaded, err = resolveManifestGraph(ctx, reloaded, d.apps...)
		}
		if err != nil {
			return fmt.Errorf("metadata resolution failed: %w", err)
		}
		if !d.updateManifest(reloaded, &change) {
			return nil
		}
		if change.contexts.Len() == 0 && d.changedImages.Len() == 0 {
			d.options.output.status("app rebuild", "no app changed; continuing to watch", progressDone)
			return nil
		}
	}
	d.stopPreparation()
	if _, err := d.manager.controlToken(ctx); err != nil {
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
		delete(d.reloaded, app.Name)
	}
	d.options.types.accept(dirty)
	d.session = updated
	d.options.output.status("app rebuild", fmt.Sprintf("remote update requested (generation %d)", updated.Generation), progressDone)
	return nil
}

func (d *devLoop) updateManifest(reloaded manifestSpec, change *devChange) bool {
	if !sameManifestTopology(d.manifest, reloaded) {
		d.options.output.problem("app metadata",
			errors.New("app topology, dependencies, configuration, egress or access rules changed; restart holon dev to apply it"))
		return false
	}
	d.options.output.problem("app metadata", nil)
	for i, app := range reloaded.Apps {
		previous := d.manifest.Apps[i]
		if app.Path != "" && !reflect.DeepEqual(app.resolved.descriptor, previous.resolved.descriptor) {
			change.contexts.Insert(app.Path)
		}
		if app.resolved.Image != previous.resolved.Image || !reflect.DeepEqual(app.Registry, previous.Registry) {
			d.changedImages.Insert(app.Name)
		}
	}
	d.manifest = reloaded
	return true
}
