package cloud

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

	"github.com/holon-sh/cli/pkg/ociimage"
)

// pushTypeMetadata keeps every direct dependency blob alive in this repository.
// The returned digest names the own document, not its liveness manifest.
func pushTypeMetadata(
	ctx context.Context, repository *remote.Repository, metadata *mcpcontract.Metadata, documents map[string]*typegen.Document,
) (ocispec.Descriptor, string, error) {
	own, err := typegen.NewDocument(ctx, metadata)
	if err != nil {
		return ocispec.Descriptor{}, "", err
	}
	blobs := []*typegen.Document{own}
	// Preparation has already resolved and verified every required digest.
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
		Layers: layers,
		// Keep identical contracts reproducible instead of adding the current time.
		ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "1970-01-01T00:00:00Z"},
	})
	descriptor.Platform = &ocispec.Platform{OS: "unknown", Architecture: "unknown"}
	return descriptor, own.Digest, err
}

func contractRepository(image string, options buildOptions) (*remote.Repository, error) {
	named, err := ociimage.Parse(image)
	if err != nil {
		return nil, err
	}
	grant := ""
	for _, path := range options.metadataGrant.Repositories {
		if named.Name() == options.metadataGrant.Registry+"/"+path {
			grant = options.metadataGrant.Grant
			break
		}
	}
	return imageRepository(named.Name(), grant, options.HTTPClient)
}

func resolveTypeMetadata(ctx context.Context, image string, options buildOptions) (string, *typegen.Document, error) {
	repository, err := contractRepository(image, options)
	if err != nil {
		return "", nil, err
	}
	descriptor, err := repository.Resolve(ctx, image)
	if err != nil {
		return "", nil, fmt.Errorf("resolve dependency contract for %s: %w", image, err)
	}
	metadata, err := typegen.ReadDocument(ctx, repository, descriptor)
	return repository.Reference.String() + "@" + descriptor.Digest.String(), metadata, err
}

// fetchTypeDocument caches the verified bytes for one preparation or comparison run.
func fetchTypeDocument(
	ctx context.Context, image, reference string, documents map[string]*typegen.Document, options buildOptions,
) (*mcpcontract.Metadata, error) {
	if document := documents[reference]; document != nil {
		return document.Metadata, nil
	}
	repository, err := contractRepository(image, options)
	if err != nil {
		return nil, err
	}
	document, err := typegen.FetchDocument(ctx, repository.Blobs(), reference)
	if err != nil {
		return nil, err
	}
	documents[reference] = document
	return document.Metadata, nil
}
