package mcptype

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// testdata/golden holds the mcptype 0.0.6 native binding's answers to every parity case.
// The port must reproduce them byte for byte and reject what the binding rejected.
func TestOutputsMatchNativeCompilerGoldens(t *testing.T) {
	for _, c := range parityCases(t) {
		got, err := runCase(c.op, c.input)
		if c.wantError {
			if err == nil {
				t.Errorf("%s: accepted an invalid request", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if c.op != "generate" {
			want, err := os.ReadFile(filepath.Join("testdata", "golden", c.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s:\n got %s\nwant %s", c.name, got, want)
			}
			continue
		}
		var response struct {
			Files map[string]string `json:"files"`
		}
		if err := json.Unmarshal([]byte(got), &response); err != nil {
			t.Fatal(err)
		}
		directory := filepath.Join("testdata", "golden", c.name)
		want := map[string][]byte{}
		if err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			relative, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			want[filepath.ToSlash(relative)], err = os.ReadFile(path)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for path, encoded := range response.Files {
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if expected, ok := want[path]; !ok {
				t.Errorf("%s: unexpected file %s", c.name, path)
			} else if !bytes.Equal(data, expected) {
				t.Errorf("%s: %s differs\n--- got\n%s\n--- want\n%s", c.name, path, data, expected)
			}
		}
		for path := range want {
			if _, ok := response.Files[path]; !ok {
				t.Errorf("%s: missing file %s", c.name, path)
			}
		}
	}
}
