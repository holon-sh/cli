package typegen

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func generatedGo(directory string) bool {
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".go") {
			return false
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil || !strings.HasPrefix(string(data), "// "+mcpcontract.Marker+" app=") {
			return false
		}
	}
	return true
}

// validateGoSlot accepts an absent or marked generated holon/ package.
func validateGoSlot(root string) error {
	directory := filepath.Join(root, "holon")
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || !generatedGo(directory) {
		return fmt.Errorf("refusing to overwrite unmarked %s", directory)
	}
	return nil
}

func writeGoSlot(root string, files map[string][]byte) error {
	if err := validateGoSlot(root); err != nil {
		return err
	}
	for name, text := range files {
		if !strings.HasSuffix(name, ".go") || !bytes.HasPrefix(text, []byte("// "+mcpcontract.Marker+" app=")) {
			return errors.New("generated Go package is missing its ownership banner")
		}
	}
	return replaceDirectory(root, filepath.Join(root, "holon"), files)
}
