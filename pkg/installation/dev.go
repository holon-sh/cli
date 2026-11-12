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

type fileWatcher interface {
	Add(name string) error
	Close() error
	events() (<-chan fsnotify.Event, <-chan error)
}

type fsnotifyWatcher struct{ *fsnotify.Watcher }

func (w fsnotifyWatcher) events() (<-chan fsnotify.Event, <-chan error) { return w.Events, w.Errors }

func watchFiles() (fileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	return fsnotifyWatcher{watcher}, err
}

type devWatcher struct {
	watcher  fileWatcher
	metadata sets.Set[string]
	contexts sets.Set[string]
	changes  chan devChange
	output   *progressOutput
	done     chan struct{}
	clock    clock.WithTicker
}

func newDevWatcher(manifest manifestSpec, watch func() (fileWatcher, error), clk clock.WithTicker, output *progressOutput) (*devWatcher, error) {
	watcher, err := watch()
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
	events, errs := w.watcher.events()
	for {
		select {
		case event, ok := <-events:
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
		case err, ok := <-errs:
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

type devLoop struct {
	manager             *Client
	name                string
	manifest            manifestSpec
	apps                []string
	clock               clock.WithTicker
	watch               func() (fileWatcher, error)
	started             time.Time
	options             buildOptions
	session             protocol.SessionResponse
	pending             sets.Set[string]
	starting            sets.Set[string]
	announce, unapplied bool
	sent                map[string]string
	background          context.Context
	recording           map[string]context.CancelFunc
	records             sync.WaitGroup
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

const watching = "Watching for changes. Ctrl-C exits and leaves this session for the next holon dev."

func (d *devLoop) run(ctx context.Context, stdout io.Writer) error {
	watcher, err := newDevWatcher(d.manifest, d.watch, d.clock, d.options.output)
	if err != nil {
		return fmt.Errorf("watch build contexts: %w", err)
	}
	defer func() { _ = watcher.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	var background sync.WaitGroup
	background.Go(func() { d.renew(ctx) })
	background.Go(func() {
		input := protocol.SessionLogsRequest{Since: d.started.Format(time.RFC3339Nano)}
		input.Session = d.name
		records := &resumingWriter{out: stdout, stdio: !d.options.output.verbose}
		if err := d.manager.streamLogs(ctx, input, records); ctx.Err() == nil {
			d.options.output.problem("log stream", fmt.Errorf("stopped: %w", err))
		}
	})
	defer func() {
		cancel()
		background.Wait()
	}()
	for ctx.Err() == nil {
		if err := d.manager.authorizeOperation(ctx, d.session); err != nil {
			return err
		}
		if err := d.settle(ctx, stdout); err != nil {
			return err
		}
		if d.pending.Len() > 0 {
			reportSessionProgress(d.session, d.options.output, "Deploying apps", progressWorking)
		} else {
			reportSessionProgress(d.session, d.options.output, watching, progressIdle)
		}
		changes := watcher.changes
		if lifecyclePending(d.session) {
			changes = nil
		}
		read, stopRead := d.readSession(ctx)
		select {
		case <-ctx.Done():
			stopRead()
			return nil
		case result := <-read:
			stopRead()
			if err := d.poll(result.session, result.err); err != nil {
				return err
			}
		case change := <-changes:
			stopRead()
			if d.rebuild(ctx, change) {
				next, err := newDevWatcher(d.manifest, d.watch, d.clock, d.options.output)
				if err != nil {
					return fmt.Errorf("watch build contexts: %w", err)
				}
				_ = watcher.Close()
				watcher = next
			}
		}
	}
	return nil
}

func (d *devLoop) settle(ctx context.Context, stdout io.Writer) error {
	if d.pending.Len() == 0 {
		return nil
	}
	d.options.output.serving(d.session, d.starting)
	ready, err := SessionReady(d.session)
	if !ready {
		return err
	}
	d.options.output.problem("MCP types", d.generate(ctx))
	d.finishPending("ready", progressDone)
	if !d.announce {
		return nil
	}
	d.announce = false
	return d.options.output.endpoints(stdout, "Ready", d.manager, d.session.Name, d.manifest.Apps, false)
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
	for _, app := range sets.List(d.pending) {
		d.options.output.status(app, phase, state)
	}
	d.pending.Clear()
}

func (d *devLoop) rebuild(ctx context.Context, change devChange) bool {
	if change.manifest || d.unapplied {
		applied, err := d.apply(ctx)
		if ctx.Err() != nil {
			return false
		}
		d.unapplied = err != nil
		d.options.output.problem("manifest", err)
		if applied {
			return true
		}
	}
	d.reload(ctx, change.contexts)
	return false
}

func (d *devLoop) apply(ctx context.Context) (bool, error) {
	manifest, err := resolveManifestFile(d.manifest.file)
	if err != nil {
		return false, err
	}
	manifest.sources, manifest.access = d.manifest.sources, d.manifest.access
	resolved, err := resolveManifestGraph(ctx, manifest, d.apps...)
	if err == nil {
		err = protocol.AccessPolicy(resolved.declarations(), d.manager.info.OAuth != nil)
	}
	if err != nil || reflect.DeepEqual(resolved.Apps, d.manifest.Apps) {
		return false, err
	}
	d.options.output.status("", "Preparing apps", progressWorking)
	if err := d.options.types.acquire(resolved); err != nil {
		return false, err
	}
	request, err := uploadManifest(ctx, d.manager, d.name, resolved, d.options)
	if err != nil {
		return false, err
	}
	d.options.output.deploying(request.Apps)
	if err := d.update(ctx, resolved, request, removedApps(d.session.Apps, request.Apps)...); err != nil {
		return false, err
	}
	d.manifest, d.announce = resolved, true
	return true, nil
}

func (d *devLoop) update(ctx context.Context, manifest manifestSpec, desired protocol.AppsRequestFor[protocol.App], remove ...string) error {
	updated, err := replaceExecution(ctx, d.manager, d.name, d.session, desired, remove...)
	if err != nil {
		d.options.output.apps(desired.Apps, "deployment failed", progressError)
		return err
	}
	for _, app := range desired.Apps {
		i := slices.IndexFunc(d.session.Apps, func(running protocol.App) bool { return running.Name == app.Name })
		if i < 0 || d.session.Apps[i].Image != app.Image || d.session.Apps[i].Overlay == app.Overlay {
			d.sent[app.Name] = app.Overlay
		} else if err := d.resend(ctx, manifest, app.Name); err != nil {
			d.options.output.status(app.Name, fmt.Sprintf("reload failed: %v", err), progressError)
			continue
		}
		d.pending.Insert(app.Name)
	}
	for _, name := range remove {
		d.options.output.status(name, "removed", progressDone)
	}
	d.session, d.starting = updated, startingApps(updated)
	return nil
}

func removedApps(running, desired []protocol.App) []string {
	var removed []string
	for _, app := range running {
		if !slices.ContainsFunc(desired, func(kept protocol.App) bool { return kept.Name == app.Name }) {
			removed = append(removed, app.Name)
		}
	}
	return removed
}
