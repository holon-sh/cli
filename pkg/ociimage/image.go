package ociimage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
)

func Copy(
	ctx context.Context, source oras.ReadOnlyTarget, destination oras.Target, image string, required ...string,
) (ocispec.Descriptor, error) {
	descriptor, err := source.Resolve(ctx, image)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("resolve source image: %w", err)
	}
	if descriptor.Digest.Validate() != nil || descriptor.Digest.Algorithm() != digest.SHA256 {
		return ocispec.Descriptor{}, errors.New("source image did not resolve to SHA-256")
	}
	var document map[string]json.RawMessage
	var children, selected []ocispec.Descriptor
	switch descriptor.MediaType {
	case ocispec.MediaTypeImageIndex, "application/vnd.docker.distribution.manifest.list.v2+json":
		if err := FetchJSON(ctx, source, descriptor, "metadata", &document); err != nil {
			return ocispec.Descriptor{}, err
		}
		if err := json.Unmarshal(document["manifests"], &children); err != nil {
			return ocispec.Descriptor{}, err
		}
	default:
		platform, err := imagePlatform(ctx, source, descriptor)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		child := descriptor
		child.Platform = &platform
		children = []ocispec.Descriptor{child}
	}
	architectures := map[string]bool{}
	for _, child := range children {
		switch {
		case child.ArtifactType == mcpcontract.MediaType:
		case child.ArtifactType == "" && child.Platform != nil && SupportedPlatform(*child.Platform):
			if architectures[child.Platform.Architecture] {
				return ocispec.Descriptor{}, errors.New("duplicate image architecture")
			}
			architectures[child.Platform.Architecture] = true
			if len(required) != 0 && !slices.Contains(required, child.Platform.Architecture) {
				continue
			}
		default:
			continue
		}
		selected = append(selected, child)
	}
	if len(architectures) == 0 {
		return ocispec.Descriptor{}, errors.New("unsupported image architecture; requires Linux amd64 or arm64")
	}
	for _, architecture := range required {
		if !architectures[architecture] {
			return ocispec.Descriptor{}, fmt.Errorf("image does not provide the installation's linux/%s architecture", architecture)
		}
	}
	if len(selected) == len(children) {
		return descriptor, copyGraph(ctx, source, destination, descriptor)
	}
	for _, child := range selected {
		if err := copyGraph(ctx, source, destination, child); err != nil {
			return ocispec.Descriptor{}, err
		}
	}
	document["manifests"], _ = json.Marshal(selected)
	data, _ := json.Marshal(document)
	return oras.PushBytes(ctx, destination, descriptor.MediaType, data)
}

func imagePlatform(ctx context.Context, source content.Fetcher, descriptor ocispec.Descriptor) (ocispec.Platform, error) {
	switch descriptor.MediaType {
	case ocispec.MediaTypeImageManifest, "application/vnd.docker.distribution.manifest.v2+json":
	case ocispec.MediaTypeImageIndex, "application/vnd.docker.distribution.manifest.list.v2+json":
		return ocispec.Platform{}, errors.New("nested image indexes are unsupported")
	default:
		return ocispec.Platform{}, fmt.Errorf("unsupported image manifest %q", descriptor.MediaType)
	}
	var manifest ocispec.Manifest
	if err := FetchJSON(ctx, source, descriptor, "metadata", &manifest); err != nil {
		return ocispec.Platform{}, err
	}
	var config ocispec.Image
	if err := FetchJSON(ctx, source, manifest.Config, "metadata", &config); err != nil {
		return ocispec.Platform{}, err
	}
	return config.Platform, nil
}

func copyGraph(ctx context.Context, source oras.ReadOnlyTarget, destination oras.Target, descriptor ocispec.Descriptor) error {
	if err := oras.CopyGraph(ctx, source, destination, descriptor, oras.DefaultCopyGraphOptions); err != nil {
		return fmt.Errorf("copy image into selected registry: %w", err)
	}
	return nil
}
