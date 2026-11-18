package appartifact

import (
	"context"
	"errors"
	"slices"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"

	"github.com/holon-sh/cli/pkg/ociimage"
)

const FrozenBy = "holon deploy"

var ErrNoSource = errors.New("holon deploy did not freeze this image")

func SourceLayer(ctx context.Context, source content.Fetcher, image ocispec.Descriptor) (ocispec.Descriptor, error) {
	var index ocispec.Index
	if err := fetchJSON(ctx, source, image, &index); err != nil {
		return ocispec.Descriptor{}, err
	}
	i := slices.IndexFunc(index.Manifests, func(child ocispec.Descriptor) bool {
		return child.Platform != nil && ociimage.SupportedPlatform(*child.Platform)
	})
	if i < 0 {
		return ocispec.Descriptor{}, ErrNoSource
	}
	var manifest ocispec.Manifest
	if err := fetchJSON(ctx, source, index.Manifests[i], &manifest); err != nil {
		return ocispec.Descriptor{}, err
	}
	var config ocispec.Image
	if err := fetchJSON(ctx, source, manifest.Config, &config); err != nil {
		return ocispec.Descriptor{}, err
	}
	made := slices.DeleteFunc(config.History, func(step ocispec.History) bool { return step.EmptyLayer })
	if len(made) == len(manifest.Layers) {
		for i, step := range slices.Backward(made) {
			if step.CreatedBy == FrozenBy {
				return manifest.Layers[i], nil
			}
		}
	}
	return ocispec.Descriptor{}, ErrNoSource
}
