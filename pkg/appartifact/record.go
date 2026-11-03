package appartifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"

	"github.com/holon-sh/cli/pkg/protocol"
)

const (
	MediaType       = "application/vnd.holon.app-build.v1+json"
	IndexType       = "application/vnd.holon.app-build.v1"
	DevelopmentTag  = "holon-dev"
	ReleasePrefix   = "holon-release-"
	RetainedTag     = "holon-retained"
	MaxDocumentSize = 1 << 20
)

var ErrIncompatible = errors.New("unsupported app build")

func Push(ctx context.Context, target oras.Target, image ocispec.Descriptor, record protocol.BuildRecord) (ocispec.Descriptor, error) {
	if err := record.Validate(); err != nil {
		return ocispec.Descriptor{}, err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if len(data) > MaxDocumentSize {
		return ocispec.Descriptor{}, errors.New("app build metadata exceeds limit")
	}
	layer, err := pushBytes(ctx, target, MediaType, data)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	metadata, err := oras.PackManifest(ctx, target, oras.PackManifestVersion1_1, IndexType, oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{layer}, ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "1970-01-01T00:00:00Z"},
	})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return PushIndex(ctx, target, IndexType, []ocispec.Descriptor{image, metadata})
}

func PushIndex(ctx context.Context, target content.Storage, artifactType string, manifests []ocispec.Descriptor) (ocispec.Descriptor, error) {
	data, err := json.Marshal(ocispec.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageIndex,
		ArtifactType: artifactType,
		Manifests:    manifests,
	})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return pushBytes(ctx, target, ocispec.MediaTypeImageIndex, data)
}

func ReadIndex(ctx context.Context, source content.Fetcher, descriptor ocispec.Descriptor) (ocispec.Descriptor, ocispec.Descriptor, error) {
	var index ocispec.Index
	if err := FetchJSON(ctx, source, descriptor, &index); err != nil {
		return ocispec.Descriptor{}, ocispec.Descriptor{}, err
	}
	if index.ArtifactType != IndexType || len(index.Manifests) != 2 {
		return ocispec.Descriptor{}, ocispec.Descriptor{}, fmt.Errorf("%w: missing metadata; rebuild with a current Holon CLI", ErrIncompatible)
	}
	image, metadata := index.Manifests[0], index.Manifests[1]
	if !protocol.ValidDigest(image.Digest.String()) || image.Size <= 0 || metadata.ArtifactType != IndexType {
		return ocispec.Descriptor{}, ocispec.Descriptor{}, fmt.Errorf("%w: invalid descriptors", ErrIncompatible)
	}
	return image, metadata, nil
}

func Read(ctx context.Context, source content.Fetcher, descriptor ocispec.Descriptor) (protocol.BuildRecord, ocispec.Descriptor, error) {
	image, metadata, err := ReadIndex(ctx, source, descriptor)
	if err != nil {
		return protocol.BuildRecord{}, ocispec.Descriptor{}, err
	}
	var manifest ocispec.Manifest
	if err := FetchJSON(ctx, source, metadata, &manifest); err != nil {
		return protocol.BuildRecord{}, ocispec.Descriptor{}, err
	}
	if manifest.ArtifactType != IndexType || len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != MediaType {
		return protocol.BuildRecord{}, ocispec.Descriptor{}, fmt.Errorf("%w: metadata format", ErrIncompatible)
	}
	var record protocol.BuildRecord
	if err := FetchJSON(ctx, source, manifest.Layers[0], &record); err != nil {
		return protocol.BuildRecord{}, ocispec.Descriptor{}, err
	}
	if err := record.Validate(); err != nil {
		return record, image, fmt.Errorf("%w: %w", ErrIncompatible, err)
	}
	return record, image, nil
}

func FetchJSON(ctx context.Context, source content.Fetcher, descriptor ocispec.Descriptor, target any) error {
	if descriptor.Size < 0 || descriptor.Size > MaxDocumentSize || !protocol.ValidDigest(descriptor.Digest.String()) {
		return fmt.Errorf("%w: invalid or oversized metadata descriptor", ErrIncompatible)
	}
	data, err := content.FetchAll(ctx, source, descriptor)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%w: invalid metadata JSON", ErrIncompatible)
	}
	return nil
}

func pushBytes(ctx context.Context, target content.Pusher, mediaType string, data []byte) (ocispec.Descriptor, error) {
	descriptor, err := oras.PushBytes(ctx, target, mediaType, data)
	if errors.Is(err, errdef.ErrAlreadyExists) {
		return content.NewDescriptorFromBytes(mediaType, data), nil
	}
	return descriptor, err
}

func Publish(ctx context.Context, repository oras.Target, image ocispec.Descriptor, record protocol.BuildRecord, tag string) (ocispec.Descriptor, error) {
	descriptor, err := Push(ctx, repository, image, record)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if tag == ReleasePrefix {
		tag += descriptor.Digest.Encoded()
	}
	if err = repository.Tag(ctx, descriptor, tag); err == nil {
		return descriptor, nil
	}
	observed, readErr := repository.Resolve(ctx, tag)
	if readErr == nil && observed.Digest == descriptor.Digest {
		return descriptor, nil
	}
	return ocispec.Descriptor{}, errors.New("app build publication was not confirmed; the image is uploaded, but deployment was not submitted")
}
