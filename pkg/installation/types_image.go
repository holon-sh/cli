package installation

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/typegen"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"
)

// pushTypeMetadata keeps every direct dependency blob alive in this repository.
// The returned digest names the own document, not its liveness manifest.
func pushTypeMetadata(
	ctx context.Context, repository *remote.Repository, metadata *mcpcontract.Metadata, documents map[string]*typegen.Document,
) (ocispec.Descriptor, string, error) {
	own, err := typegen.NewDocument(metadata)
	if err != nil {
		return ocispec.Descriptor{}, "", err
	}
	blobs := []*typegen.Document{own}
	for _, alias := range slices.Sorted(maps.Keys(metadata.Requires)) {
		blobs = append(blobs, documents[metadata.Requires[alias]])
	}
	layers := make([]ocispec.Descriptor, 0, len(blobs))
	for _, blob := range blobs {
		layer, err := oras.PushBytes(ctx, repository, mcpcontract.MediaType, blob.Data)
		if err != nil {
			return ocispec.Descriptor{}, "", err
		}
		layers = append(layers, layer)
	}
	descriptor, err := oras.PackManifest(ctx, repository, oras.PackManifestVersion1_1, mcpcontract.MediaType, oras.PackManifestOptions{
		Layers:              layers,
		ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "1970-01-01T00:00:00Z"},
	})
	descriptor.Platform = &ocispec.Platform{OS: "unknown", Architecture: "unknown"}
	return descriptor, own.Digest, err
}

func imageExpectations(ctx context.Context, image string, options buildOptions) (string, map[string]*mcpcontract.Metadata, error) {
	repository, err := sourceRepository(image, options.HTTPClient)
	if err != nil {
		return "", nil, err
	}
	descriptor, err := repository.Resolve(ctx, image)
	if err != nil {
		return "", nil, fmt.Errorf("resolve %s: %w", image, err)
	}
	pinned := repository.Reference.String() + "@" + descriptor.Digest.String()
	own, err := typegen.ReadDocument(ctx, repository, descriptor)
	if own == nil || err != nil {
		return pinned, nil, err
	}
	expects := map[string]*mcpcontract.Metadata{}
	for alias, reference := range own.Requires {
		expected, err := typegen.FetchDocument(ctx, repository.Blobs(), reference)
		if err != nil {
			return "", nil, fmt.Errorf("%s: fetch expected %s contract %s: %w", image, alias, reference, err)
		}
		expects[alias] = expected.Metadata
	}
	return pinned, expects, nil
}
