package typegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"os"
	"path/filepath"
	"strings"
)

func legacyManaged(directory string) bool {
	var pkg struct {
		Holon string `json:"holon"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	return err == nil && json.Unmarshal(data, &pkg) == nil && pkg.Holon == mcpcontract.Marker
}
func legacyManagedFiles(files map[string]string) bool {
	var pkg struct {
		Holon string `json:"holon"`
	}
	return json.Unmarshal([]byte(files["package.json"]), &pkg) == nil && pkg.Holon == mcpcontract.Marker
}
func generatedPython(directory string) bool {
	data, err := os.ReadFile(filepath.Join(directory, "apps", "__init__.py"))
	return err == nil && bytes.HasPrefix(data, []byte("# "+mcpcontract.Marker+" app="))
}
func validatePythonSlot(root string) error {
	for _, path := range []string{"holon.py", "holon", "holon/apps", "holon/apps/__init__.py"} {
		info, err := os.Lstat(filepath.Join(root, path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if path == "holon.py" || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing Python slot because %s exists or is a symlink", filepath.Join(root, path))
		}
	}
	holon := filepath.Join(root, "holon")
	if legacyManaged(holon) {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(holon, "__init__.py")); err == nil {
		return errors.New("holon/__init__.py prevents namespace imports; remove it before generating types")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Lstat(filepath.Join(holon, "apps", "__init__.py")); err == nil && !generatedPython(holon) {
		return errors.New("refusing to overwrite unmarked holon/apps/__init__.py")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func writePythonSlot(root string, files map[string]string) error {
	if err := validatePythonSlot(root); err != nil {
		return err
	}
	holon := filepath.Join(root, "holon")
	if legacyManaged(holon) {
		if err := os.RemoveAll(holon); err != nil {
			return err
		}
	}
	for name, content := range files {
		if name != "holon/apps/__init__.py" {
			return fmt.Errorf("unexpected Python generated file %q", name)
		}
		if !strings.HasPrefix(content, "# "+mcpcontract.Marker+" app=") {
			return errors.New("generated Python module is missing its ownership banner")
		}
		target := filepath.Join(root, name)
		if old, err := os.ReadFile(target); err == nil && string(old) == content {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		temp, err := os.CreateTemp(filepath.Dir(target), ".holon-generated-")
		if err != nil {
			return err
		}
		_, err = temp.WriteString(content)
		err = errors.Join(err, temp.Chmod(0o644), temp.Close())
		if err == nil {
			err = os.Rename(temp.Name(), target)
		}
		_ = os.Remove(temp.Name())
		if err != nil {
			return err
		}
	}
	return nil
}
