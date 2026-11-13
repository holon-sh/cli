// Package mcptype compares MCP typed contracts and generates typed client packages.
package mcptype

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// ParseMetadata decodes a contract document, or reports why data is not one.
func ParseMetadata(data []byte) (*mcpcontract.Metadata, error) {
	var document mcpcontract.Metadata
	if err := jsonv2.Unmarshal(data, &document, jsonv2.RejectUnknownMembers(true), jsontext.AllowInvalidUTF8(true)); err != nil {
		return nil, err
	}
	if _, err := decodeMetadata(&document); err != nil {
		return nil, err
	}
	return &document, nil
}

// SameContract reports whether two contracts are equal once annotations are removed and unordered
// keywords sorted.
func SameContract(a, b *mcpcontract.Contract) (bool, error) {
	var forms [2]string
	for i, side := range []*mcpcontract.Contract{a, b} {
		decoded, err := decodeContract(*side)
		if err != nil {
			return false, err
		}
		canonicalContract(decoded)
		forms[i] = string(encode(decoded))
	}
	return forms[0] == forms[1], nil
}

// GenerateRequest asks for one target's typed client package: node, browser, python or go.
// Empty optional strings use their defaults; GoModule is required for Go, and App for a browser
// client of its own app.
type GenerateRequest struct {
	Target    string                           `json:"target"`
	Identity  mcpcontract.Identity             `json:"identity"`
	Requires  map[string]mcpcontract.Identity  `json:"requires,omitempty"`
	Contracts map[string]*mcpcontract.Contract `json:"contracts,omitempty"`
	Own       string                           `json:"own,omitempty"`
	// App is the name Holon serves the own app under, which a browser page connects to at /mcp/<App>.
	App string `json:"app,omitempty"`
	// Banner is the first-line comment on generated sources.
	Banner   string `json:"banner,omitempty"`
	GoModule string `json:"go_module,omitempty"`
}
