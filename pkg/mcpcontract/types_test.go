package mcpcontract

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStableBuildInputs(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "version")
	if err := WriteStable(path, []byte("1.2.3"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(StableTime([]byte("1.2.3"))) {
		t.Fatalf("unstable mtime: %v %v", info, err)
	}
	if StableTime([]byte("1.2.3")).Equal(StableTime([]byte("1.2.4"))) {
		t.Fatal("changed content kept its mtime")
	}
}
