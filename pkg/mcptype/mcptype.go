// Package mcptype compares MCP typed contracts and generates typed client packages.
package mcptype

import (
	"context"
	jsonv2 "encoding/json/v2"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// ValidateMetadata reports why data is not a contract document, or nil when it is one.
func ValidateMetadata(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var document mcpcontract.Metadata
	if err := jsonv2.Unmarshal(data, &document, jsonv2.RejectUnknownMembers(true)); err != nil {
		return err
	}
	_, err := decodeMetadata(&document)
	return err
}

// SameContract reports whether two contracts are equal once annotations are removed and unordered
// keywords sorted.
func SameContract(ctx context.Context, a, b *mcpcontract.Contract) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
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
// Empty optional strings use their defaults; GoModule is required for Go.
type GenerateRequest struct {
	Target    string                           `json:"target"`
	Identity  mcpcontract.Identity             `json:"identity"`
	Requires  map[string]mcpcontract.Identity  `json:"requires,omitempty"`
	Contracts map[string]*mcpcontract.Contract `json:"contracts,omitempty"`
	Own       string                           `json:"own,omitempty"`
	// Banner is the first-line comment on generated sources.
	Banner   string `json:"banner,omitempty"`
	GoModule string `json:"go_module,omitempty"`
}
