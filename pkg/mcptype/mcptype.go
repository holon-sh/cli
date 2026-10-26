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
	canonical, digest, err := canonicalRequest(value)
	if err != nil || canonical == nil {
		return CanonicalForm{}, err
	}
	return CanonicalForm{Canonical: encode(canonical), Digest: digest}, nil
}

// canonicalRequest answers {"contract": Contract}, {"schema": {...}} or {"metadata": Metadata};
// the canonical form is nil for metadata without a contract.
func canonicalRequest(value Value) (Value, string, error) {
	object, err := asObject(value)
	if err != nil {
		return nil, "", err
	}
	if len(object) != 1 {
		return nil, "", fmt.Errorf("expected a map with a single key, one of `contract`, `schema`, `metadata`")
	}
	variant := sortedKeys(object)[0]
	contract := func(contract Contract) (Value, string, error) {
		form := canonicalContract(contract).toValue()
		return form, hash(form), nil
	}
	switch variant {
	case "contract":
		document, err := decodeContract(object[variant])
		if err != nil {
			return nil, "", err
		}
		return contract(document)
	case "schema":
		schema, err := asObject(object[variant])
		if err != nil {
			return nil, "", err
		}
		normalizeObject(schema)
		return schema, hash(schema), nil
	case "metadata":
		metadata, err := decodeMetadata(object[variant])
		if err != nil {
			return nil, "", err
		}
		if metadata.Schema == nil {
			return nil, "", nil
		}
		return contract(*metadata.Schema)
	}
	return nil, "", fmt.Errorf("unknown variant `%s`, expected one of `contract`, `schema`, `metadata`", variant)
}

// Changes lists what breaks expected if published is deployed. Empty means compatible.
func Changes(ctx context.Context, expected *mcpcontract.Metadata, published *mcpcontract.Metadata) ([]Change, error) {
	value, err := marshal(ctx, map[string]any{"expected": expected, "published": published})
	if err != nil {
		return nil, err
	}
	return changesRequest(value)
}

// changesRequest answers {"expected": Metadata, "published": Metadata | null}.
func changesRequest(value Value) ([]Change, error) {
	object, err := asObject(value)
	if err != nil {
		return nil, err
	}
	raw, err := requiredField(object, "expected")
	if err != nil {
		return nil, err
	}
	expected, err := decodeMetadata(raw)
	if err != nil {
		return nil, err
	}
	var published *Metadata
	if raw := object["published"]; raw != nil {
		metadata, err := decodeMetadata(raw)
		if err != nil {
			return nil, err
		}
		published = &metadata
	}
	if expected.Schema == nil {
		return nil, fmt.Errorf("expected document has no schema")
	}
	return changes(&expected, published), nil
}

// GenerateRequest asks for one target's typed client package: node, browser, python or go.
// Runtime files are copied into the package verbatim.
type GenerateRequest struct {
	Target                  string                           `json:"target"`
	Identity                mcpcontract.Identity             `json:"identity"`
	Requires                map[string]mcpcontract.Identity  `json:"requires,omitempty"`
	Contracts               map[string]*mcpcontract.Contract `json:"contracts,omitempty"`
	Runtime                 map[string][]byte                `json:"runtime,omitempty"`
	Protocol                string                           `json:"protocol,omitempty"`
	RuntimeSpecifier        string                           `json:"runtime_specifier,omitempty"`
	BrowserRuntimeSpecifier string                           `json:"browser_runtime_specifier,omitempty"`
	ProtocolSpecifier       string                           `json:"protocol_specifier,omitempty"`
	PythonModule            string                           `json:"python_module,omitempty"`
	PythonRuntimeModule     string                           `json:"python_runtime_module,omitempty"`
	Banner                  string                           `json:"banner,omitempty"`
	GoModule                string                           `json:"go_module,omitempty"`
	// Entry names the TypeScript entry point files: "index" gives index.js, index.d.ts and resources.d.ts.
	// Empty means index for node and browser for browser.
	Entry string `json:"entry,omitempty"`
}

// Generate returns the package files by relative path.
func Generate(ctx context.Context, request GenerateRequest) (map[string][]byte, error) {
	value, err := marshal(ctx, request)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeGenerateRequest(value)
	if err != nil {
		return nil, err
	}
	return generate(ctx, decoded)
}

// marshal takes the request through its JSON wire form, the request representation
// decodeGenerateRequest validates.
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
