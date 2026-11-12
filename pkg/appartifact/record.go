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

func Publish(ctx context.Context, repository oras.Target, image ocispec.Descriptor, record protocol.BuildRecord, tag string) error {
	if err := record.Validate(); err != nil {
		return err
	}
	data, _ := json.Marshal(record)
	if len(data) > maxDocumentSize {
		return errors.New("app build metadata exceeds limit")
	}
	layer, err := oras.PushBytes(ctx, repository, mediaType, data)
	if err != nil {
		return err
	}
	metadata, err := oras.PackManifest(ctx, repository, oras.PackManifestVersion1_1, indexType, oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{layer}, ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "1970-01-01T00:00:00Z"},
	})
	if err != nil {
		return err
	}
	index, _ := json.Marshal(ocispec.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageIndex,
		ArtifactType: indexType,
		Manifests:    []ocispec.Descriptor{image, metadata},
	})
	descriptor, err := oras.PushBytes(ctx, repository, ocispec.MediaTypeImageIndex, index)
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
