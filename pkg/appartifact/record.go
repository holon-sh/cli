package appartifact

import (
	"context"
	"encoding/json"
	"errors"

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
		tag = releaseTag(descriptor)
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

// Build is an image's published build metadata.
type Build struct {
	Metadata ocispec.Descriptor // the index Publish pushed
	Image    ocispec.Descriptor
	Record   protocol.BuildRecord
}

// Read returns the build metadata that reference names in target.
func Read(ctx context.Context, target oras.ReadOnlyTarget, reference string) (Build, error) {
	metadata, err := target.Resolve(ctx, reference)
	if err != nil {
		return Build{}, err
	}
	var index ocispec.Index
	if err := fetchDocument(ctx, target, metadata, &index); err != nil {
		return Build{}, err
	}
	if index.ArtifactType != indexType || len(index.Manifests) != 2 {
		return Build{}, errors.New("not app build metadata")
	}
	var manifest ocispec.Manifest
	if err := fetchDocument(ctx, target, index.Manifests[1], &manifest); err != nil {
		return Build{}, err
	}
	if len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != mediaType {
		return Build{}, errors.New("not app build metadata")
	}
	build := Build{Metadata: metadata, Image: index.Manifests[0]}
	if err := fetchDocument(ctx, target, manifest.Layers[0], &build.Record); err != nil {
		return Build{}, err
	}
	return build, build.Record.Validate()
}

// Release tags build metadata already in target as a release, under the name Publish gives one.
func Release(ctx context.Context, target oras.Target, metadata ocispec.Descriptor) error {
	return target.Tag(ctx, metadata, releaseTag(metadata))
}

func releaseTag(metadata ocispec.Descriptor) string {
	return ReleasePrefix + metadata.Digest.Encoded()
}

func fetchDocument(ctx context.Context, target content.Fetcher, descriptor ocispec.Descriptor, value any) error {
	if descriptor.Size <= 0 || descriptor.Size > maxDocumentSize {
		return errors.New("app build metadata exceeds limit")
	}
	data, err := content.FetchAll(ctx, target, descriptor)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
