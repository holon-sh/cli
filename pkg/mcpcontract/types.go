// Package mcpcontract holds the typed-contract wire format Holon publishes in image
// metadata and helpers for generated files. Comparison and generation live in mcptype.
package mcpcontract

import "encoding/json"

const (
	Marker           = "holon-generated-apps-v1"
	DigestAnnotation = "sh.holon.contract.digest"
	MediaType        = "application/vnd.holon.mcp-contract.v1+json"
)

type Tool struct {
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"inputSchema"`
	Output json.RawMessage `json:"outputSchema,omitempty"`
}

type Resource struct {
	URI    string          `json:"uri"`
	Output json.RawMessage `json:"outputSchema"`
}

type Contract struct {
	Tools     []Tool     `json:"tools"`
	Resources []Resource `json:"resources,omitempty"`
}

// MarshalJSON writes "tools" as an array even when there are none, as the v1
// contract document requires.
func (c Contract) MarshalJSON() ([]byte, error) {
	type wire Contract
	if c.Tools == nil {
		c.Tools = []Tool{}
	}
	return json.Marshal(wire(c))
}

type Identity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Metadata is the contract document stored as an OCI blob. Requires pins each
// dependency to its exact document bytes in the consumer's repository.
type Metadata struct {
	Identity Identity          `json:"identity"`
	Schema   *Contract         `json:"schema,omitempty"`
	Requires map[string]string `json:"requires,omitempty"`
}
