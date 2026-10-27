package typegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedSourceSnapshot(t *testing.T) {
	root := t.TempDir()
	app, shared := filepath.Join(root, "app"), filepath.Join(root, "shared")
	put(t, app, "server.py", "from shared import value\n")
	put(t, app, ".dockerignore", "shared/private\n")
	put(t, shared, "__init__.py", "value = 1\n")
	put(t, shared, "private", "excluded")
	put(t, shared, ".venv/pyvenv.cfg", "excluded")
	if err := os.Symlink("../shared", filepath.Join(app, "shared")); err != nil {
		t.Fatal(err)
	}
	if _, err := SnapshotSource("app", app, ""); err == nil || !strings.Contains(err.Error(), "symlink escapes application") {
		t.Fatalf("undeclared shared source: %v", err)
	}
	destination := t.TempDir()
	fingerprint, err := SnapshotSource("app", app, destination, shared)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(destination, "shared")); err != nil || !info.IsDir() {
		t.Fatalf("shared source was not materialized: %v", err)
	}
	for _, name := range []string{"private", ".venv"} {
		if _, err := os.Stat(filepath.Join(destination, "shared", name)); !os.IsNotExist(err) {
			t.Fatalf("excluded shared file %s: %v", name, err)
		}
	}
	put(t, shared, "__init__.py", "value = 2\n")
	changed, err := SnapshotSource("app", app, "", shared)
	if err != nil || changed == fingerprint {
		t.Fatalf("shared edit did not change fingerprint: %v", err)
	}
	if got := read(t, destination, "shared/__init__.py"); got != "value = 1\n" {
		t.Fatalf("snapshot was not isolated: %q", got)
	}
	if err := os.Symlink("../app", filepath.Join(shared, "cycle")); err != nil {
		t.Fatal(err)
	}
	if _, err := SnapshotSource("app", app, "", shared, app); err == nil || !strings.Contains(err.Error(), "cyclic shared source") {
		t.Fatalf("shared source cycle: %v", err)
	}
}
