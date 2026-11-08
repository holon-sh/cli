// Package tooling embeds the public SDK extraction and checking tools.
package tooling

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/tooling/python"
)

// The TypeScript runner stays one archive: the Go module zip would drop its vendored node_modules.
//
//go:embed cli-tooling-ts.zip
var typescript []byte

// Unpack writes one language's runners, "typescript" or "python", to root/<language>, which must not
// exist yet.
func Unpack(root, language string) error {
	var files fs.FS
	switch language {
	case "typescript":
		archive, err := zip.NewReader(bytes.NewReader(typescript), int64(len(typescript)))
		if err != nil {
			return err
		}
		files = archive
	case "python":
		files = python.Files
	default:
		return fmt.Errorf("no %s tooling", language)
	}
	return os.CopyFS(filepath.Join(root, language), files)
}

// UnpackChecker writes what an image build runs to check an application to root/<language>, which
// must not exist yet. TypeScript needs only check.mjs, which imports nothing but Node builtins.
func UnpackChecker(root, language string) error {
	if language != "typescript" {
		return Unpack(root, language)
	}
	archive, err := zip.NewReader(bytes.NewReader(typescript), int64(len(typescript)))
	if err != nil {
		return err
	}
	checker, err := fs.ReadFile(archive, "check.mjs")
	if err != nil {
		return err
	}
	directory := filepath.Join(root, language)
	if err := os.Mkdir(directory, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "check.mjs"), checker, 0o644)
}
