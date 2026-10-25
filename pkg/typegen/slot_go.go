package typegen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// validateGoSlot accepts an absent, empty or Holon-marked holon/ package directory.
func validateGoSlot(root string) error {
	directory := filepath.Join(root, "holon")
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing Go slot because %s is not a directory", directory)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) > 0 && !legacyManaged(directory) {
		return fmt.Errorf("refusing to overwrite unmarked %s", directory)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return fmt.Errorf("refusing Go slot entry %s", filepath.Join(directory, entry.Name()))
		}
	}
	return nil
}
func writeGoSlot(root string, files map[string]string) error {
	if err := validateGoSlot(root); err != nil {
		return err
	}
	if !legacyManagedFiles(files) {
		return errors.New("generated Go package is missing its ownership marker")
	}
	return replaceDirectory(root, filepath.Join(root, "holon"), files)
}
