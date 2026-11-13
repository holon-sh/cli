// Package tooling embeds the public SDK extraction and checking tools.
package tooling

import (
	"archive/zip"
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/holon-sh/cli/tooling/python"
)

// The TypeScript runner stays one archive: the Go module zip would drop its vendored node_modules.
//
//go:embed cli-tooling-ts.zip
var typescript []byte

// Language names the embedded scripts and interpreters used on the host and in an app image.
type Language struct {
	Program, Runner, Checker string
	Flags, Programs, Probe   []string
}

var Languages = map[string]Language{
	"typescript": {"node", "runner.mjs", "check.mjs", []string{"--experimental-vm-modules", "--no-warnings"}, []string{"node"}, []string{"-e", "process.exit(Number(process.versions.node.split('.')[0]) < 22 ? 1 : 0)"}},
	"python":     {"python", "extract.py", "runner.py", nil, []string{"python3", "python3.15", "python3.14", "python3.13", "python3.12", "python3.11"}, []string{"-I", "-c", "import sys; sys.exit(sys.version_info < (3, 11))"}},
}

// LocalProgram finds a supported host interpreter. Images use Program from their installed environment.
func (l Language) LocalProgram(ctx context.Context) (string, error) {
	for _, program := range l.Programs {
		if exec.CommandContext(ctx, program, l.Probe...).Run() == nil {
			return program, nil
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", fmt.Errorf("install a supported interpreter (%s)", strings.Join(l.Programs, ", "))
}

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
