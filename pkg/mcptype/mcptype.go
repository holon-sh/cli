// Package mcptype compares MCP typed contracts and generates typed client packages.
package mcptype

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// Change is one way a published contract breaks an expectation. Name is the tool name or resource URI, if any.
type Change struct {
	Kind string
	Name string
	Text string
}

// ValidateMetadata reports why data is not a contract document, or nil when it is one.
func ValidateMetadata(ctx context.Context, data []byte) error {
	value, err := marshal(ctx, json.RawMessage(data))
	if err == nil {
		_, err = decodeMetadata(value)
	}
	return err
}

// SameContract reports whether two contracts are equal once annotations are removed and unordered
// keywords sorted.
func SameContract(ctx context.Context, a, b *mcpcontract.Contract) (bool, error) {
	var forms [2]string
	for i, side := range []*mcpcontract.Contract{a, b} {
		value, err := marshal(ctx, side)
		if err != nil {
			return false, err
		}
		decoded, err := decodeContract(value)
		if err != nil {
			return false, err
		}
		forms[i] = string(encode(canonicalContract(decoded)))
	}
	return forms[0] == forms[1], nil
}

// Changes lists what breaks expected if published is deployed. Empty means compatible.
func Changes(ctx context.Context, expected *mcpcontract.Metadata, published *mcpcontract.Metadata) ([]Change, error) {
	value, err := marshal(ctx, map[string]any{"expected": expected, "published": published})
	if err != nil {
		return nil, err
	}
	object := value.(Object) // Both document keys are supplied above.
	expectedMetadata, err := decodeMetadata(object["expected"])
	if err != nil {
		return nil, err
	}
	var publishedMetadata *metadata
	if raw := object["published"]; raw != nil {
		metadata, err := decodeMetadata(raw)
		if err != nil {
			return nil, err
		}
		publishedMetadata = &metadata
	}
	if expectedMetadata.Schema == nil {
		return nil, fmt.Errorf("expected document has no schema")
	}
	return changes(&expectedMetadata, publishedMetadata), nil
}

// GenerateRequest asks for one target's typed client package: node, browser, python or go.
// Empty optional strings use their defaults; GoModule is required for Go.
type GenerateRequest struct {
	Target    string                           `json:"target"`
	Identity  mcpcontract.Identity             `json:"identity"`
	Requires  map[string]mcpcontract.Identity  `json:"requires,omitempty"`
	Contracts map[string]*mcpcontract.Contract `json:"contracts,omitempty"`
	// RuntimeSpecifier is the module TypeScript clients import their runtime and its declarations from.
	RuntimeSpecifier string `json:"runtime_specifier,omitempty"`
	Own              string `json:"own,omitempty"`
	// Banner is the first-line comment on generated sources.
	Banner   string `json:"banner,omitempty"`
	GoModule string `json:"go_module,omitempty"`
}

// marshal decodes a private JSON tree, preserving schema number semantics.
func marshal(ctx context.Context, request any) (Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	return decodeJSON(data)
}
