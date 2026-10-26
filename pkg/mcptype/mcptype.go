// Package mcptype compares MCP typed contracts and generates typed client packages.
// It is a Go port of the mcptype compiler (hughhan1/mcptype 0.0.6) and produces the
// same bytes: canonical forms, digests, change texts and package files.
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
		if metadata.Contract == nil {
			return nil, "", nil
		}
		return contract(*metadata.Contract)
	}
	return nil, "", fmt.Errorf("unknown variant `%s`, expected one of `contract`, `schema`, `metadata`", variant)
}

// Changes lists what breaks expected if published is deployed. Empty means compatible.
func Changes(ctx context.Context, expected mcpcontract.Expectation, published *mcpcontract.Metadata) ([]Change, error) {
	value, err := marshal(ctx, map[string]any{"expected": expected, "published": published})
	if err != nil {
		return nil, err
	}
	found, err := changesRequest(value)
	if err != nil {
		return nil, err
	}
	result := make([]Change, len(found))
	for i, change := range found {
		result[i] = Change{Kind: change.kind, Name: change.name + change.uri, Text: change.text()}
	}
	return result, nil
}

// changesRequest answers {"expected": Expectation, "published": Metadata | null}.
func changesRequest(value Value) ([]change, error) {
	object, err := asObject(value)
	if err != nil {
		return nil, err
	}
	raw, err := requiredField(object, "expected")
	if err != nil {
		return nil, err
	}
	expected, err := decodeExpectation(raw)
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
	return changes(expected, published), nil
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
	PackageFields           map[string]any                   `json:"package_fields,omitempty"`
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
	// Own, for browser targets, is the contracts key of the app's own server. The module then exports
	// App: the ext-apps App with that server's tools and resources typed, called through the host.
	Own string `json:"own,omitempty"`
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

// marshal takes the request through its JSON wire form, which validates it exactly as the
// compiler's JSON entry points do.
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
