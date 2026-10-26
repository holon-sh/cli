// Package mcptype compares MCP typed contracts and generates typed client packages.
package mcptype

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/opencontainers/go-digest"
)

// Change is one way a published contract breaks an expectation. Name is the tool name or resource URI, if any.
type Change struct {
	Kind string
	Name string
	Text string
}

// CanonicalForm is a document with annotations removed and unordered keywords sorted.
// Both fields are empty for metadata without a contract.
type CanonicalForm struct {
	Canonical json.RawMessage `json:"canonical"`
	Digest    string          `json:"digest"`
}

// Canonical validates document, one of {"contract"}, {"schema"} or {"metadata"}, and returns its canonical form and digest.
func Canonical(ctx context.Context, document any) (CanonicalForm, error) {
	value, err := marshal(ctx, document)
	if err != nil {
		return CanonicalForm{}, err
	}
	object, err := asObject(value)
	if err != nil {
		return CanonicalForm{}, err
	}
	if len(object) != 1 {
		return CanonicalForm{}, fmt.Errorf("expected a map with a single key, one of `contract`, `schema`, `metadata`")
	}
	variant := sortedKeys(object)[0]
	var canonical Value
	switch variant {
	case "contract":
		contract, err := decodeContract(object[variant])
		if err != nil {
			return CanonicalForm{}, err
		}
		canonical = canonicalContract(contract)
	case "schema":
		schema, err := asObject(object[variant])
		if err != nil {
			return CanonicalForm{}, err
		}
		normalizeObject(schema)
		canonical = schema
	case "metadata":
		metadata, err := decodeMetadata(object[variant])
		if err != nil || metadata.Schema == nil {
			return CanonicalForm{}, err
		}
		canonical = canonicalContract(*metadata.Schema)
	default:
		return CanonicalForm{}, fmt.Errorf("unknown variant `%s`, expected one of `contract`, `schema`, `metadata`", variant)
	}
	data := encode(canonical)
	return CanonicalForm{Canonical: data, Digest: digest.FromBytes(data).String()}, nil
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
	var publishedMetadata *Metadata
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
// Runtime files are copied into the package verbatim. Empty optional strings use
// their defaults; GoModule is required for Go.
type GenerateRequest struct {
	Target    string                           `json:"target"`
	Identity  mcpcontract.Identity             `json:"identity"`
	Requires  map[string]mcpcontract.Identity  `json:"requires,omitempty"`
	Contracts map[string]*mcpcontract.Contract `json:"contracts,omitempty"`
	Runtime   map[string][]byte                `json:"runtime,omitempty"`
	// Protocol is inlined into TypeScript declarations unless ProtocolSpecifier imports them.
	Protocol                string `json:"protocol,omitempty"`
	RuntimeSpecifier        string `json:"runtime_specifier,omitempty"`
	BrowserRuntimeSpecifier string `json:"browser_runtime_specifier,omitempty"`
	ProtocolSpecifier       string `json:"protocol_specifier,omitempty"`
	// PythonModule defaults to holon/apps/__init__.py; PythonRuntimeModule to holon.client.
	PythonModule        string `json:"python_module,omitempty"`
	PythonRuntimeModule string `json:"python_runtime_module,omitempty"`
	// Banner is the first-line comment on generated sources, excluding copied Runtime files.
	Banner   string `json:"banner,omitempty"`
	GoModule string `json:"go_module,omitempty"`
	// Entry names the TypeScript entry point files: "index" gives index.js, index.d.ts and resources.d.ts.
	// Empty means index for node and browser for browser.
	Entry string `json:"entry,omitempty"`
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
