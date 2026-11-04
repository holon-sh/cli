package appartifact

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"

	"github.com/holon-sh/protocol"
)

const (
	mediaType       = "application/vnd.holon.app-build.v1+json"
	indexType       = "application/vnd.holon.app-build.v1"
	DevelopmentTag  = "holon-dev"
	ReleasePrefix   = "holon-release-"
	maxDocumentSize = 1 << 20
)

func push(ctx context.Context, target oras.Target, image ocispec.Descriptor, record protocol.BuildRecord) (ocispec.Descriptor, error) {
	if err := record.Validate(); err != nil {
		return ocispec.Descriptor{}, err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if len(data) > maxDocumentSize {
		return ocispec.Descriptor{}, errors.New("app build metadata exceeds limit")
	}
	layer, err := oras.PushBytes(ctx, target, mediaType, data)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	metadata, err := oras.PackManifest(ctx, target, oras.PackManifestVersion1_1, indexType, oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{layer}, ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "1970-01-01T00:00:00Z"},
	})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	index, err := json.Marshal(ocispec.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageIndex,
		ArtifactType: indexType,
		Manifests:    []ocispec.Descriptor{image, metadata},
	})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return oras.PushBytes(ctx, target, ocispec.MediaTypeImageIndex, index)
}

func Publish(ctx context.Context, repository oras.Target, image ocispec.Descriptor, record protocol.BuildRecord, tag string) error {
	descriptor, err := push(ctx, repository, image, record)
	if err != nil {
		return err
	}
	if tag == ReleasePrefix {
		tag += descriptor.Digest.Encoded()
	}
	if err = repository.Tag(ctx, descriptor, tag); err == nil {
		return nil
	}
	observed, readErr := repository.Resolve(ctx, tag)
	if readErr == nil && observed.Digest == descriptor.Digest {
		return nil
	}
	return errors.New("app build publication was not confirmed; the image is uploaded, but deployment was not submitted")
}
