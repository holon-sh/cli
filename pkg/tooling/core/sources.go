package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fingerprint(root, language string, paths map[string]string, ignored []string) (map[string]string, error) {
	if language == "python" {
		// Python has always guarded all source files, including unimported modules.
		if paths == nil {
			paths = map[string]string{}
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path != root && slices.Contains(ignored, entry.Name()) {
					return filepath.SkipDir
				}
			} else if filepath.Ext(path) == ".py" {
				name, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				name = filepath.ToSlash(name)
				if _, read := paths[name]; !read {
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					paths[name] = string(data)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	// Workers return the text they actually parsed. Hash that text, then verify
	// the files, so a concurrent edit cannot pair an old AST with a new hash.
	for path, contents := range paths {
		if !filepath.IsLocal(path) {
			return nil, fmt.Errorf("contract source must stay inside the application: %s", path)
		}
		paths[path] = digest([]byte(contents))
	}
	return paths, Verify(root, paths)
}

// Verify rejects missing or changed source files using their original bytes, so
// line ending normalization cannot invalidate a freshly extracted Python contract.
// Absolute paths remain supported for existing Python runner callers.
func Verify(root string, sources map[string]string) error {
	for path, expected := range sources {
		file := path
		if !filepath.IsAbs(file) {
			file = filepath.Join(root, file)
		}
		actual, err := fileHash(file)
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("%s: application install/build changed a source file used for its MCP contract; refusing to publish stale types", path)
		}
	}
	return nil
}
