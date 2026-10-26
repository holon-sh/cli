package mcptype

import (
	"bytes"
	"flag"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

var updateSnapshots = flag.Bool("update", false, "rewrite expected compiler package snapshots")

// Snapshots belong to the Go generator. Review intentional output changes after
// running go test ./pkg/mcptype -run TestSnapshots -update.
func TestSnapshots(t *testing.T) {
	for _, c := range generationCases(t) {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.run()
			if c.wantError {
				if err == nil {
					t.Fatal("accepted an invalid generation request")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join("testdata", "golden", c.name)
			if *updateSnapshots {
				if err := os.RemoveAll(directory); err != nil {
					t.Fatal(err)
				}
				for name, content := range got {
					path := filepath.Join(directory, name)
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, content, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			want := map[string][]byte{}
			if err := fs.WalkDir(os.DirFS(directory), ".", func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				want[path], err = fs.ReadFile(os.DirFS(directory), path)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if !maps.EqualFunc(got, want, bytes.Equal) {
				t.Fatal("generated files differ; review changes with go test ./pkg/mcptype -run TestSnapshots -update")
			}
		})
	}
}
