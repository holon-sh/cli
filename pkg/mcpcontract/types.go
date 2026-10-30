// Package mcpcontract holds the typed-contract wire format Holon publishes in image
// metadata and helpers for generated files. Comparison and generation live in mcptype.
package mcpcontract

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"os"
	"time"
)

const (
	Marker           = "holon-generated-apps-v1"
	DigestAnnotation = "sh.holon.contract.digest"
	MediaType        = "application/vnd.holon.mcp-contract.v1+json"
)

// StableTime returns the mtime Holon gives a file it writes into a build
// context. BuildKit's incremental sync resends a file only when its size or
// mtime changed, so the time must be fixed for unchanged content and differ
// for changed content of the same size. It is 1980 (the zip epoch) plus a
// content-derived offset of under 105 days.
func StableTime(data []byte) time.Time {
	sum := sha256.Sum256(data)
	return time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(binary.BigEndian.Uint64(sum[:]) >> 11))
}

// WriteStable writes data to path with mode and StableTime(data) as its mtime.
func WriteStable(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chtimes(path, StableTime(data), StableTime(data))
}

type Tool struct {
	Name        string          `json:"name"`
	Input       json.RawMessage `json:"inputSchema"`
	Output      json.RawMessage `json:"outputSchema,omitempty"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
	Meta        json.RawMessage `json:"_meta,omitempty"`
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
