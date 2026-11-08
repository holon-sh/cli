package tooling

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackCheckerWritesOnlyTheTypeScriptChecker(t *testing.T) {
	root := t.TempDir()
	if err := UnpackChecker(root, "typescript"); err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadDir(filepath.Join(root, "typescript"))
	if err != nil || len(staged) != 1 || staged[0].Name() != "check.mjs" {
		t.Fatalf("staged TypeScript tooling: %v %v", staged, err)
	}
}
