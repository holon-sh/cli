// Package tooling embeds the shared Go core and language extraction/checking workers.
package tooling

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed cli-tooling-*.zip SHA256SUMS
var assets embed.FS

// Unpack verifies the archives and writes language workers and compatibility hosts.
func Unpack(root string) error {
	sums, err := assets.ReadFile("SHA256SUMS")
	if err != nil {
		return err
	}
	entries, err := fs.Glob(assets, "cli-tooling-*.zip")
	if err != nil {
		return err
	}
	if len(entries) != 3 {
		return errors.New("expected core, TypeScript and Python tooling archives")
	}
	for _, name := range entries {
		data, err := assets.ReadFile(name)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		if !strings.Contains(string(sums), hex.EncodeToString(hash[:])+"  "+name+"\n") {
			return fmt.Errorf("tooling checksum mismatch: %s", name)
		}
		language := "typescript"
		if strings.HasPrefix(name, "cli-tooling-py-") {
			language = "python"
		} else if name == "cli-tooling-core.zip" {
			language = "core"
		}
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, file := range archive.File {
			if !filepath.IsLocal(file.Name) || file.Mode()&os.ModeSymlink != 0 {
				return errors.New("invalid tooling path")
			}
			path := filepath.Join(root, language, file.Name)
			if file.FileInfo().IsDir() {
				if err := os.MkdirAll(path, 0o755); err != nil {
					return err
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			content, err := fs.ReadFile(archive, file.Name)
			if err != nil {
				return err
			}
			if err = os.WriteFile(path, content, 0o644|file.Mode().Perm()&0o111); err != nil {
				return err
			}
		}
	}
	return nil
}
