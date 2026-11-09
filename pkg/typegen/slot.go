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

// contractVersion is the generated-client contract the holon-sh and holon-ui packages declare as
// holon.contractVersion: clients live in the project, in generated/holon-sh or generated/holon-ui,
// and import the runtime as holon-sh/runtime or holon-ui/runtime.
const contractVersion = 2

var errNotInstalled = errors.New("not installed")

// banner opens every generated TypeScript and Go file.
var banner = []byte("// " + mcpcontract.Marker + " app=")

// installedRuntime verifies that pkg, resolved upward from root as Node.js resolves it, runs the
// clients this CLI generates.
func installedRuntime(root, pkg string) error {
	for dir := root; ; dir = filepath.Dir(dir) {
		manifest := filepath.Join(dir, "node_modules", pkg, "package.json")
		var installed struct {
			Version string
			Holon   struct {
				ContractVersion int `json:"contractVersion"`
			}
		}
		err := readTypeJSON(manifest, &installed)
		switch {
		case errors.Is(err, os.ErrNotExist) && filepath.Dir(dir) != dir:
			continue
		case errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("%s is %w for %s; install it before generating types", pkg, errNotInstalled, root)
		case err != nil:
			return err
		}
		name := strings.TrimSpace(pkg + " " + installed.Version)
		switch {
		case installed.Holon.ContractVersion < contractVersion:
			return fmt.Errorf("%s keeps generated clients inside node_modules; upgrade it to a release that reads them from the project (holon.contractVersion %d)", name, contractVersion)
		case installed.Holon.ContractVersion > contractVersion:
			return fmt.Errorf("%s expects generated client contract %d; this holon writes contract %d, so upgrade holon", name, installed.Holon.ContractVersion, contractVersion)
		}
		return nil
	}
}

// SlotDirectory is the directory a target's generated clients occupy in the project at dir.
func SlotDirectory(dir, target string) string {
	switch target {
	case TargetPy:
		return filepath.Join(dir, "holon", "apps")
	case TargetGo:
		return filepath.Join(dir, "holon")
	}
	return filepath.Join(dir, "generated", nodePackages[target])
}

// generatedDirectory reports whether directory holds only regular files that open with the generated
// clients' banner.
func generatedDirectory(directory string) bool {
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return false
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil || !bytes.HasPrefix(data, banner) {
			return false
		}
	}
	return true
}

// validateGenerated accepts an absent, empty or generated directory.
func validateGenerated(directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if entries, err := os.ReadDir(directory); !info.IsDir() || err != nil || len(entries) != 0 && !generatedDirectory(directory) {
		return fmt.Errorf("refusing to overwrite unmarked %s", directory)
	}
	return nil
}

// writeGenerated replaces directory with files, each of which carries the generated clients' banner.
func writeGenerated(directory string, files map[string][]byte) error {
	if err := validateGenerated(directory); err != nil {
		return err
	}
	for _, text := range files {
		if !bytes.HasPrefix(text, banner) {
			return fmt.Errorf("generated %s is missing its ownership banner", directory)
		}
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0o755); err != nil {
		return err
	}
	return replaceDirectory(filepath.Dir(directory), directory, files)
}

// generatedSlot reports whether the project at dir holds a target's generated clients.
func generatedSlot(dir, target string) bool {
	if target == TargetPy {
		return generatedPython(filepath.Join(dir, "holon"))
	}
	return generatedDirectory(SlotDirectory(dir, target))
}

func validateSlot(target, dir string) error {
	if target == TargetPy {
		return validatePythonSlot(dir)
	}
	return validateGenerated(SlotDirectory(dir, target))
}

func writeSlot(target, dir string, files map[string][]byte) error {
	if target == TargetPy {
		return writePythonSlot(dir, files)
	}
	return writeGenerated(SlotDirectory(dir, target), files)
}

// replaceDirectory atomically replaces directory, inside parent, with exactly files.
// An identical directory is left untouched.
func replaceDirectory(parent, directory string, files map[string][]byte) error {
	same := true
	for name, content := range files {
		old, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || !bytes.Equal(old, content) {
			same = false
			break
		}
	}
	if same {
		entries, err := os.ReadDir(directory)
		if err == nil && len(entries) == len(files) {
			return nil
		}
	}
	temp, err := os.MkdirTemp(parent, ".holon-generated-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err := os.Chmod(temp, 0o755); err != nil {
		return err
	}
	for name, content := range files {
		if filepath.Base(name) != name || strings.HasPrefix(name, ".") {
			return fmt.Errorf("invalid generated file %q", name)
		}
		if err := os.WriteFile(filepath.Join(temp, name), content, 0o644); err != nil {
			return err
		}
	}
	backup, err := os.MkdirTemp(parent, ".holon-previous-")
	if err != nil {
		return err
	}
	if err = os.Remove(backup); err != nil {
		return err
	}
	moved := false
	if err = os.Rename(directory, backup); err == nil {
		moved = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.Rename(temp, directory); err != nil {
		if moved {
			err = errors.Join(err, os.Rename(backup, directory))
		}
		return err
	}
	return os.RemoveAll(backup)
}
