package typegen

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// AppConsumer is the consumer for a project app kept in dir, without its dependencies: the slots its
// SDK dependencies declare and, when dir keeps an MCP server, that server. It reports false for an
// app with neither, which has nothing to generate or check.
func AppConsumer(name, dir string) (Consumer, bool, error) {
	entry, err := ServerEntry(dir)
	if err != nil {
		return Consumer{}, false, err
	}
	targets, err := DeclaredTargets(dir)
	if err != nil && !errors.Is(err, ErrNoRuntime) {
		return Consumer{}, false, err
	}
	c := Consumer{Name: name, Dir: dir, Targets: targets}
	if entry != "" {
		c.Server = &Source{Path: filepath.Join(dir, entry)}
	}
	return c, len(targets) != 0 || entry != "", nil
}

// ServerEntry is the MCP server entry kept in root, or "" for an app that is not an MCP server: one
// without a server entry, or JavaScript that never mentions an MCP SDK or holon-sh.
func ServerEntry(root string) (string, error) {
	entry, err := SourceEntry(root)
	if err != nil {
		return "", err
	}
	if (strings.HasSuffix(entry, ".ts") || strings.HasSuffix(entry, ".js")) && !mentionsMCP(root) {
		return "", nil
	}
	return entry, nil
}

// mentionsMCP reports whether any JavaScript or TypeScript source under root imports an MCP SDK or holon-sh.
func mentionsMCP(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if entry.IsDir() {
			if GeneratedSource(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if slices.Contains([]string{".ts", ".mts", ".cts", ".js", ".mjs", ".cjs", ".tsx", ".jsx"}, filepath.Ext(path)) {
			data, err := os.ReadFile(path)
			found = err == nil && (bytes.Contains(data, []byte("@modelcontextprotocol/")) || bytes.Contains(data, []byte(nodePackages[TargetTS])))
		}
		return nil
	})
	return found
}
