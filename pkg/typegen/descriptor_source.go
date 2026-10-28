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

// ReadDescriptor resolves a server source's MCP descriptor (server.json): an authored server.json
// wins, and otherwise the descriptor is derived from the source's static MCP constructor metadata
// and its publishing declaration, when it has one. It returns nil without error when the source
// has no derivable metadata.
func ReadDescriptor(ctx context.Context, dir string, options Options) (data json.RawMessage, err error) {
	defer func() {
		if len(data) != 0 {
			_, err = ParseDescriptor(data)
		}
		if err != nil {
			err = fmt.Errorf("descriptor: %w", err)
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if data, err = authoredDescriptor(dir); err != nil || data != nil {
		return data, err
	}
	entry, err := SourceEntry(dir)
	language := sourceLanguage(entry)
	if err != nil || entry == "" || language == "go" || !MentionsMCP(dir) {
		return nil, err
	}
	root, err := os.MkdirTemp("", "holon-descriptor-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	snapshot := filepath.Join(root, "source")
	if _, err = SnapshotSource("source", dir, snapshot); err != nil {
		return nil, err
	}
	if err = tooling.Unpack(filepath.Join(root, "tooling")); err != nil {
		return nil, err
	}
	s := &preparedSource{origin: dir, root: snapshot, entry: entry, language: language}
	return s.extractDescriptor(ctx, root, options)
}

// authoredDescriptor reads dir's authored server.json, or returns nil when there is none.
func authoredDescriptor(dir string) (json.RawMessage, error) {
	path := filepath.Join(dir, "server.json")
	if err := regularNoSymlink(path, "server descriptor"); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimited(file, MaxDescriptorBytes)
}

// extractDescriptor derives the source's descriptor from its immutable snapshot (s.root), using
// the extractors under root/tooling. It returns the raw descriptor; callers add the directory
// context to errors.
func (s *preparedSource) extractDescriptor(ctx context.Context, root string, options Options) (json.RawMessage, error) {
	if !isFile(filepath.Join(s.root, s.entry)) {
		return nil, fmt.Errorf("%s is excluded from its own source snapshot", s.entry)
	}
	results, err := extractTypes(ctx, root, map[string][]extractionEntry{
		s.language: {{Name: "server", Path: s.root, Entry: s.entry, Descriptor: true}},
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
