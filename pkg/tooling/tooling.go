// Package tooling embeds the public SDK extraction and checking tools.
package tooling

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/tooling/python"
)

// The TypeScript runner stays one archive: the Go module zip would drop its vendored node_modules.
//
//go:embed cli-tooling-ts.zip
var typescript []byte

// Unpack writes each language's runner to its own directory, which must not exist yet.
func Unpack(root string) error {
	archive, err := zip.NewReader(bytes.NewReader(typescript), int64(len(typescript)))
	if err != nil {
		return err
	}
	if err := os.CopyFS(filepath.Join(root, "typescript"), archive); err != nil {
		return err
	}
	return os.CopyFS(filepath.Join(root, "python"), python.Files)
}

// File reads one of a language's files without unpacking the rest.
func File(language, name string) ([]byte, error) {
	if language == "python" {
		return python.Files.ReadFile(name)
	}
	archive, err := zip.NewReader(bytes.NewReader(typescript), int64(len(typescript)))
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(archive, name)
}
