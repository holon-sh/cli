package mcptype

import (
	"bytes"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/golden holds the mcptype 0.0.6 native binding's answers to every parity case.
// The port must reproduce them byte for byte and reject what the binding rejected.
func TestOutputsMatchNativeCompilerGoldens(t *testing.T) {
	for _, c := range parityCases(t) {
		got, err := c.run()
		if c.wantError != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantError) {
				t.Errorf("%s: expected %q, got %v", c.name, c.wantError, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		directory, pattern := filepath.Join("testdata", "golden"), c.name+".json"
		if c.op == "generate" {
			directory, pattern = filepath.Join(directory, c.name), "."
		}
		want := map[string][]byte{}
		if err := fs.WalkDir(os.DirFS(directory), pattern, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			want[path], err = fs.ReadFile(os.DirFS(directory), path)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if !maps.EqualFunc(got, want, bytes.Equal) {
			t.Errorf("%s: generated files differ\n--- got\n%q\n--- want\n%q", c.name, got, want)
		}
	}
}
