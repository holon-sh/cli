package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/opencontainers/go-digest"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry"
)

const maxContractBytes = 4 << 20

// Document retains the bytes a registry digest names. Copy Data when publishing;
// marshaling Metadata again would give a different digest for some valid documents.
type Document struct {
	*mcpcontract.Metadata
	Data   []byte
	Digest string
}

// ParseDocument validates a document and retains its exact bytes.
func ParseDocument(ctx context.Context, data []byte) (*Document, error) {
	if len(data) > maxContractBytes {
		return nil, fmt.Errorf("contract document exceeds %d bytes", maxContractBytes)
	}
	if _, err := mcptype.Canonical(ctx, map[string]any{"metadata": json.RawMessage(data)}); err != nil {
		return nil, err
	}
	var metadata mcpcontract.Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}
	return &Document{Metadata: &metadata, Data: bytes.Clone(data), Digest: digest.FromBytes(data).String()}, nil
}

// NewDocument serializes a newly observed contract with Go's sorted JSON keys,
// including keys inside schemas. Numeric schema values retain their precision.
func NewDocument(ctx context.Context, metadata *mcpcontract.Metadata) (*Document, error) {
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	data, err = json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return ParseDocument(ctx, data)
}

// FetchDocument reads a blob by digest, bounds its size and verifies its bytes.
// The same reader serves an image's own document and its pinned dependencies.
func FetchDocument(ctx context.Context, blobs registry.BlobStore, reference string) (*Document, error) {
	expected, err := digest.Parse(reference)
	if err != nil || expected.Algorithm() != digest.SHA256 {
		return nil, fmt.Errorf("contract requires a SHA-256 digest: %q", reference)
	}
	descriptor, reader, err := blobs.FetchReference(ctx, reference)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if descriptor.Size <= 0 || descriptor.Size > maxContractBytes {
		return nil, fmt.Errorf("invalid contract document size %d", descriptor.Size)
	}
	// Verify the requested digest even for a caller-supplied BlobStore.
	descriptor.Digest = expected
	data, err := content.ReadAll(reader, descriptor)
	if err != nil {
		return nil, fmt.Errorf("contract blob %s failed verification: %w", reference, err)
	}
	return ParseDocument(ctx, data)
}
