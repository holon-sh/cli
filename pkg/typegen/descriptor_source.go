package typegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/tooling"
)

// ReadDescriptor prefers an authored server.json. Otherwise it derives the descriptor from
// the MCP constructor and optional publishing declaration, using the existing static extractors.
// It never executes application code or writes into the source directory. No supported server
// entry returns nil; an invalid descriptor or unsupported expression returns an error.
func ReadDescriptor(ctx context.Context, dir string, options Options) (data json.RawMessage, err error) {
	defer func() {
		if err == nil && len(data) != 0 {
			_, err = ParseDescriptor(data)
		}
		if err != nil {
			err = fmt.Errorf("descriptor: %w", err)
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "server.json")
	err = regularNoSymlink(path, "server descriptor")
	if err == nil {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return readLimited(file, MaxDescriptorBytes)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	entry, err := SourceEntry(dir)
	if err != nil || entry == "" || filepath.Ext(entry) == ".go" || !MentionsMCP(dir) {
		return nil, err
	}
	root, err := os.MkdirTemp("", "holon-descriptor-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	snapshot := filepath.Join(root, "source")
	if _, err = SnapshotSource("server", dir, snapshot); err != nil {
		return nil, err
	}
	if err = tooling.Unpack(filepath.Join(root, "tooling")); err != nil {
		return nil, err
	}
	if !isFile(filepath.Join(snapshot, entry)) {
		return nil, fmt.Errorf("%s is excluded from its own source snapshot", entry)
	}
	language := "typescript"
	if filepath.Ext(entry) == ".py" {
		language = "python"
	}
	results, err := extractTypes(ctx, root, map[string][]extractionEntry{
		language: {{Name: "server", Path: snapshot, Entry: entry, Descriptor: true}},
	}, options.defaults())
	if err != nil {
		return nil, err
	}
	result := results["server"]
	if result.Diagnostic != "" {
		return nil, fmt.Errorf("%s; provide server.json for dynamic metadata", result.Diagnostic)
	}
	var descriptor map[string]any
	if err = json.Unmarshal(result.Descriptor, &descriptor); err != nil {
		return nil, err
	}
	descriptor["$schema"] = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"
	return json.MarshalIndent(descriptor, "", "  ")
}
