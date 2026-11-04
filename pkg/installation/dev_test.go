package installation

import (
	"bytes"
	"io"
	"reflect"
	"testing"

	"github.com/fsnotify/fsnotify"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"
)

func TestDevWatcherReportsWithoutChangeConsumer(t *testing.T) {
	var stream bytes.Buffer
	output, _, _ := newProgressOutput(io.Discard, &stream, false, clock.RealClock{})
	events := make(chan fsnotify.Event)
	watcher := devWatcher{
		watcher: &fsnotify.Watcher{Events: events, Errors: make(chan error)},
		output:  output, done: make(chan struct{}),
	}
	go watcher.run()
	for _, err := range []error{fsnotify.ErrEventOverflow, io.ErrUnexpectedEOF} {
		watcher.watcher.Errors <- err
	}
	close(events)
	<-watcher.done
	want := "file watcher continuing after error: " + fsnotify.ErrEventOverflow.Error() + "\n" +
		"file watcher continuing after error: unexpected EOF\n"
	if stream.String() != want {
		t.Fatalf("watcher diagnostics = %q, want %q", stream.String(), want)
	}
}

func TestDevRegistryPackageEditsScheduleImports(t *testing.T) {
	for _, test := range []struct {
		name                    string
		edit                    func(*manifestApp)
		dirty, blocked, pending bool
	}{
		{name: "unchanged"},
		{name: "retry unchanged failed import", dirty: true, pending: true},
		{name: "server version", dirty: true, edit: func(app *manifestApp) { app.Registry.Version = "2.0.0" }},
		{name: "server identity", dirty: true, edit: func(app *manifestApp) { app.Registry.Name = "com.example/other" }},
		{name: "package selection", dirty: true, edit: func(app *manifestApp) { app.Registry.Package.Identifier = "echo" }},
		{name: "topology requires restart", blocked: true, edit: func(app *manifestApp) {
			app.Registry.Version = "2.0.0"
			app.resolved.Port = 8080
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous, reloaded := registryPackageManifest(t, "npm"), registryPackageManifest(t, "npm")
			if test.edit != nil {
				test.edit(&reloaded.Apps[0])
			}
			dev := devLoop{manifest: previous, changedImages: sets.New[string]()}
			if test.pending {
				dev.changedImages.Insert("echo")
			}
			change := devChange{manifest: true, contexts: sets.New[string]()}
			if accepted := dev.updateManifest(reloaded, &change); accepted == test.blocked {
				t.Fatalf("manifest accepted = %t, restart required = %t", accepted, test.blocked)
			}
			if dev.changedImages.Has("echo") != test.dirty || change.contexts.Len() != 0 {
				t.Fatalf("scheduled imports = %v, build contexts = %v", dev.changedImages, change.contexts)
			}
			if test.blocked && !reflect.DeepEqual(dev.manifest, previous) {
				t.Fatal("blocked edit replaced the active manifest")
			}
		})
	}
}
