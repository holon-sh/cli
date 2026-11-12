package installation

import (
	"context"
	"net/http"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/holon-sh/cli/pkg/ociimage"
)

func importImage(ctx context.Context, image, registryHost, repository, grant string, client *http.Client) (string, error) {
	source, err := sourceRepository(image, client)
	if err != nil {
		return "", err
	}
	destination, err := imageRepository(registryHost+"/"+repository, grant, client)
	if err != nil {
		return "", err
	}
	descriptor, err := ociimage.Copy(ctx, source, destination, image)
	if err != nil {
		return "", err
	}
	return registryHost + "/" + repository + "@" + descriptor.Digest.String(), nil
}

func sourceRepository(image string, client *http.Client) (*remote.Repository, error) {
	named, err := ociimage.Parse(image)
	if err != nil {
		return nil, err
	}
	return imageRepository(named.Name(), "", client)
}
