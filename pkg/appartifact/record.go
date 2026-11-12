package appartifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"

	"github.com/holon-sh/protocol"
)

const (
	mediaType       = "application/vnd.holon.app-build.v1+json"
	indexType       = "application/vnd.holon.app-build.v1"
	DevelopmentTag  = "holon-dev"
	ReleasePrefix   = "holon-release-"
	MaxDocumentSize = 1 << 20
)

var ErrIncompatible = errors.New("unsupported app build")

func ReleaseTag(buildDigest digest.Digest) string {
	return ReleasePrefix + buildDigest.Encoded()
}

func Publish(ctx context.Context, repository oras.Target, image ocispec.Descriptor, record protocol.BuildRecord, plane protocol.Plane) (ocispec.Descriptor, error) {
	if err := record.Validate(); err != nil {
		return ocispec.Descriptor{}, err
	}
	data, _ := json.Marshal(record)
	if len(data) > MaxDocumentSize {
		return ocispec.Descriptor{}, errors.New("app build metadata exceeds limit")
	}
	layer, err := oras.PushBytes(ctx, repository, mediaType, data)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	metadata, err := oras.PackManifest(ctx, repository, oras.PackManifestVersion1_1, indexType, oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{layer}, ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "1970-01-01T00:00:00Z"},
	})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	index := indexBytes(indexType, []ocispec.Descriptor{image, metadata})
	descriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, index)
	tag := DevelopmentTag
	if plane == protocol.AdminPlane {
		tag = ReleaseTag(descriptor.Digest)
	}
	if _, err := oras.TagBytes(ctx, repository, ocispec.MediaTypeImageIndex, index, tag); err == nil {
		return descriptor, nil
	}
	observed, err := repository.Resolve(ctx, tag)
	if err == nil && observed.Digest == descriptor.Digest {
		return descriptor, nil
	}
	return ocispec.Descriptor{}, errors.New("app build publication was not confirmed; the image is uploaded, but deployment was not submitted")
}

func PushIndex(ctx context.Context, target oras.Target, artifactType string, manifests []ocispec.Descriptor, tags ...string) (ocispec.Descriptor, error) {
	return oras.TagBytesN(ctx, target, ocispec.MediaTypeImageIndex, indexBytes(artifactType, manifests), tags, oras.DefaultTagBytesNOptions)
}

func indexBytes(artifactType string, manifests []ocispec.Descriptor) []byte {
	data, _ := json.Marshal(ocispec.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageIndex,
		ArtifactType: artifactType,
		Manifests:    manifests,
	})
	return data
}

func ReadIndex(ctx context.Context, source content.Fetcher, descriptor ocispec.Descriptor) (ocispec.Descriptor, ocispec.Descriptor, error) {
	var index ocispec.Index
	if err := fetchJSON(ctx, source, descriptor, &index); err != nil {
		return ocispec.Descriptor{}, ocispec.Descriptor{}, err
	}
	if index.ArtifactType != indexType || len(index.Manifests) != 2 {
		return ocispec.Descriptor{}, ocispec.Descriptor{}, fmt.Errorf("%w: missing metadata; rebuild with a current Holon CLI", ErrIncompatible)
	}
	image, metadata := index.Manifests[0], index.Manifests[1]
	if !protocol.ValidDigest(image.Digest.String()) {
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
	if err := fetchJSON(ctx, source, metadata, &manifest); err != nil {
		return protocol.BuildRecord{}, ocispec.Descriptor{}, err
	}
	if manifest.ArtifactType != indexType || len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != mediaType {
		return protocol.BuildRecord{}, ocispec.Descriptor{}, fmt.Errorf("%w: metadata format", ErrIncompatible)
	}
	var record protocol.BuildRecord
	if err := fetchJSON(ctx, source, manifest.Layers[0], &record); err != nil {
		return protocol.BuildRecord{}, ocispec.Descriptor{}, err
	}
	if err := record.Validate(); err != nil {
		return protocol.BuildRecord{}, ocispec.Descriptor{}, fmt.Errorf("%w: %w", ErrIncompatible, err)
	}
	return record, image, nil
}

func fetchJSON(ctx context.Context, source content.Fetcher, descriptor ocispec.Descriptor, target any) error {
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
