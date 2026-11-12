package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallReplacesOnlyAMatchingArchive(t *testing.T) {
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	files := tar.NewWriter(gz)
	for _, name := range []string{"LICENSE", "holon"} {
		if err := files.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len("new " + name))}); err != nil {
			t.Fatal(err)
		}
		if _, err := files.Write([]byte("new " + name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := files.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("holon_1.0.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive.Bytes())

	for _, test := range []struct {
		sums, want string
		fails      bool
	}{
		{sums: hex.EncodeToString(sum[:]) + "  " + name + "\n", want: "new holon"},
		{sums: hex.EncodeToString(make([]byte, sha256.Size)) + "  " + name + "\n", want: "old", fails: true},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1.0.0/SHA256SUMS":
				_, _ = w.Write([]byte(test.sums))
			case "/v1.0.0/" + name:
				_, _ = w.Write(archive.Bytes())
			default:
				http.NotFound(w, r)
			}
		}))
		path := filepath.Join(t.TempDir(), "holon")
		if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		err := install(t.Context(), server.URL, "1.0.0", path)
		server.Close()
		if (err != nil) != test.fails {
			t.Fatalf("install error = %v, want failure %v", err, test.fails)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != test.want {
			t.Fatalf("holon = %q, want %q", got, test.want)
		}
		if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
			t.Fatalf("left %d files beside holon, want none", len(entries)-1)
		}
	}
}
