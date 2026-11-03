package cloud

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

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
