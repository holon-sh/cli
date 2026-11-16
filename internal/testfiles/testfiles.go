package testfiles

import (
	"os"
	"path/filepath"
	"testing"
)

// Write writes files, keyed by slash-separated paths under root, creating the directories they need.
func Write[T string | []byte](t *testing.T, root string, files map[string]T) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Read returns the contents of the file at the slash-separated path name under root.
func Read(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
