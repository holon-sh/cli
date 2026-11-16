package installation

import (
	"context"
	"net/http"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/holon-sh/cli/pkg/ociimage"
)

func importImage(ctx context.Context, image string, destination *remote.Repository, client *http.Client) (ocispec.Descriptor, error) {
	source, err := sourceRepository(image, client)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return ociimage.Copy(ctx, source, destination, image)
}

func sourceRepository(image string, client *http.Client) (*remote.Repository, error) {
	named, err := ociimage.Parse(image)
	if err != nil {
		return nil, err
	}
	return imageRepository(named.Name(), "", client)
}
