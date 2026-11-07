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
// SDK dependencies declare and, when dir keeps an MCP server, that server. It returns nil for an app
// with neither, which has nothing to generate or check.
func AppConsumer(name, dir string) (*Consumer, error) {
	entry, err := SourceEntry(dir)
	if err != nil {
		return nil, err
	}
	targets, err := DeclaredTargets(dir)
	if err != nil && !errors.Is(err, ErrNoRuntime) {
		return nil, err
	}
	c := &Consumer{Name: name, Dir: dir, Targets: targets}
	// JavaScript that never mentions an MCP SDK or holon-sh is not an MCP server. The server is the
	// directory, so a sibling that depends on this app shares its extraction.
	javascript := strings.HasSuffix(entry, ".ts") || strings.HasSuffix(entry, ".js")
	if entry != "" && (!javascript || mentionsMCP(dir)) {
		c.Server = &Source{Path: dir}
	}
	if len(targets) == 0 && c.Server == nil {
		return nil, nil
	}
	return c, nil
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
