package typegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"os"
	"path/filepath"
	"strings"
)

const nodePackage = "holon-sh"

type slotMetadata struct {
	Marker      string               `json:"marker"`
	Placeholder bool                 `json:"placeholder,omitempty"`
	App         string               `json:"app,omitempty"`
	Identity    mcpcontract.Identity `json:"identity,omitempty"`
	Digests     map[string]string    `json:"digests,omitempty"`
}

// nodeSlot returns the real slot directory and the import specifier used by the app.
func nodeSlot(root string) (string, string, error) {
	for dir := root; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "node_modules", nodePackage)
		real, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			var pkg struct {
				Holon struct {
					Slot            string `json:"slot"`
					ContractVersion int    `json:"contractVersion"`
				} `json:"holon"`
			}
			if err := readTypeJSON(filepath.Join(real, "package.json"), &pkg); err != nil {
				return "", "", err
			}
			if pkg.Holon.Slot != "generated" || pkg.Holon.ContractVersion != mcpcontract.Version {
				return "", "", fmt.Errorf("%s does not declare a compatible holon.slot", candidate)
			}
			return real, nodePackage, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return "", "", fmt.Errorf("%s is not installed for %s; install it before generating types", nodePackage, root)
}
func validateNodeSlot(pkg string, meta slotMetadata) error {
	directory := filepath.Join(pkg, "generated")
	if info, err := os.Lstat(directory); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("refusing foreign generated slot %s", directory)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) || err == nil && len(entries) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	var old slotMetadata
	if err := regularNoSymlink(filepath.Join(directory, "holon-generated.json"), "slot marker"); err != nil {
		return fmt.Errorf("refusing unmarked generated slot %s: %w", directory, err)
	}
	if readTypeJSON(filepath.Join(directory, "holon-generated.json"), &old) != nil || old.Marker != mcpcontract.Marker {
		return fmt.Errorf("refusing foreign generated slot %s", directory)
	}
	if !old.Placeholder && (old.App != meta.App) {
		return fmt.Errorf("%s is owned by app %s; install a separate %s for each app", directory, old.App, nodePackage)
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing generated file symlink %s", entry.Name())
		}
	}
	return nil
}
func writeNodeSlot(pkg string, files map[string]string, meta slotMetadata) error {
	if err := validateNodeSlot(pkg, meta); err != nil {
		return err
	}
	directory := filepath.Join(pkg, "generated")
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	files = cloneFiles(files)
	files["holon-generated.json"] = string(data) + "\n"
	return replaceDirectory(pkg, directory, files)
}

// replaceDirectory atomically replaces directory, inside parent, with exactly files.
// An identical directory is left untouched.
func replaceDirectory(parent, directory string, files map[string]string) error {
	same := true
	for name, content := range files {
		old, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(old) != content {
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
	for name, content := range files {
		if filepath.Base(name) != name || strings.HasPrefix(name, ".") {
			return fmt.Errorf("invalid generated file %q", name)
		}
		if err := os.WriteFile(filepath.Join(temp, name), []byte(content), 0o644); err != nil {
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
	if moved {
		return os.RemoveAll(backup)
	}
	return nil
}
func cloneFiles(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
