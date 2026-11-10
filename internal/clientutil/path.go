package clientutil

import "path/filepath"

// PathInside reports whether path is dir or lies under it, once both have their symlinks resolved.
func PathInside(path, dir string) bool {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	relative, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(relative)
}
