package cloud

import (
	"context"
	"net/http"

	"github.com/holon-sh/cli/pkg/ociimage"
)

func importImage(ctx context.Context, image, registryHost, repository, grant string, extraEnv []string, client *http.Client) (string, error) {
	named, err := ociimage.Parse(image)
	if err != nil {
		return "", err
	}
	source, err := imageRepository(named.Name(), "", extraEnv, client)
	if err != nil {
		return "", err
	}
	destination, err := imageRepository(registryHost+"/"+repository, grant, extraEnv, client)
	if err != nil {
		return "", err
	}
	descriptor, err := ociimage.Copy(ctx, source, destination, image)
	if err != nil {
		return "", err
	}
	return registryHost + "/" + repository + "@" + descriptor.Digest.String(), nil
}
