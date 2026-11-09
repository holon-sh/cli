package typegen

import (
	"bytes"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// ContractVersion is the generated-client contract the holon-sh and holon-ui packages declare as
// holon.contractVersion: clients live in the project, in generated/holon-sh or generated/holon-ui,
// and import the runtime as holon-sh/runtime or holon-ui/runtime.
const ContractVersion = 2

// slots are the project directories each target's generated clients occupy.
var slots = map[string]string{TargetTS: "generated/holon-sh", TargetUI: "generated/holon-ui", TargetPy: "holon/apps", TargetGo: "holon"}

// generatedTemp prefixes the directories a slot is written through, which sources leave out.
const generatedTemp = ".holon-generated-"

// ancestors yields dir and each directory above it, up to the filesystem root.
func ancestors(dir string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for yield(dir) && filepath.Dir(dir) != dir {
			dir = filepath.Dir(dir)
		}
	}
}

// installedRuntime verifies that pkg, resolved upward from root as Node.js resolves it, runs the
// clients this CLI generates.
func installedRuntime(root, pkg string) error {
	for dir := range ancestors(root) {
		var installed packageJSON
		err := readTypeJSON(filepath.Join(dir, "node_modules", pkg, "package.json"), &installed)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		name := strings.TrimSpace(pkg + " " + installed.Version)
		switch {
		case installed.Holon.ContractVersion < ContractVersion:
			return fmt.Errorf("%s keeps generated clients inside node_modules; upgrade it to a release that reads them from the project (holon.contractVersion %d)", name, ContractVersion)
		case installed.Holon.ContractVersion > ContractVersion:
			return fmt.Errorf("%s expects generated client contract %d; this holon writes contract %d, so upgrade holon", name, installed.Holon.ContractVersion, ContractVersion)
		}
		return nil
	}
	return fmt.Errorf("%s is not installed for %s; install it before generating types", pkg, root)
}

// SlotDirectory is the directory a target's generated clients occupy in the project at dir.
func SlotDirectory(dir, target string) string {
	return filepath.Join(dir, filepath.FromSlash(slots[target]))
}

// slotEntries lists a slot's entries other than Python's bytecode cache.
func slotEntries(directory string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(directory)
	return slices.DeleteFunc(entries, func(entry os.DirEntry) bool { return entry.Name() == "__pycache__" }), err
}

// owned reports whether data opens with the banner mcptype writes after a // or # comment leader.
func owned(data []byte) bool {
	for _, leader := range []string{"// ", "# "} {
		if rest, ok := bytes.CutPrefix(data, []byte(leader)); ok {
			return bytes.HasPrefix(rest, []byte(mcpcontract.Marker+" app="))
		}
	}
	return false
}

// generatedSlot reports whether the project at dir holds a target's generated clients: regular files
// that each carry the generated banner.
func generatedSlot(dir, target string) bool {
	directory := SlotDirectory(dir, target)
	entries, err := slotEntries(directory)
	if err != nil || len(entries) == 0 {
		return false
	}
	return !slices.ContainsFunc(entries, func(entry os.DirEntry) bool {
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		return !entry.Type().IsRegular() || err != nil || !owned(data)
	})
}

// validateSlot accepts a target's slot in the project at dir when it is absent, empty or generated,
// and reached through real directories rather than symlinks that could lead out of the project.
// Python's holon must also stay a namespace package.
func validateSlot(dir, target string) error {
	if target == TargetPy {
		for _, name := range []string{"holon.py", filepath.Join("holon", "__init__.py")} {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				return fmt.Errorf("%s shadows the holon namespace package; remove it before generating types", name)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	path := dir
	for _, name := range strings.Split(slots[target], "/") {
		path = filepath.Join(path, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("refusing to write generated clients through %s, which is not a directory", path)
		}
	}
	if entries, err := slotEntries(path); err != nil || len(entries) != 0 && !generatedSlot(dir, target) {
		return fmt.Errorf("refusing to overwrite unmarked %s", path)
	}
	return nil
}

// writeSlot atomically replaces a target's slot in the project at dir with exactly files. An identical
// slot is left untouched.
func writeSlot(dir, target string, files map[string][]byte) error {
	if err := validateSlot(dir, target); err != nil {
		return err
	}
	directory := SlotDirectory(dir, target)
	entries, err := slotEntries(directory)
	if err == nil && len(entries) == len(files) && !slices.ContainsFunc(entries, func(entry os.DirEntry) bool {
		old, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		content, ok := files[entry.Name()]
		return err != nil || !ok || !bytes.Equal(old, content)
	}) {
		return nil
	}
	parent := filepath.Dir(directory)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(parent, generatedTemp)
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
	backup, err := os.MkdirTemp(parent, generatedTemp)
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
