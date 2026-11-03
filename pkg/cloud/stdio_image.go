package cloud

import (
	"context"
	"embed"
	"io/fs"
	"sync"

	"github.com/distribution/reference"

	"github.com/holon-sh/cli/pkg/ociimage"
)

//go:embed bridge-payloads/*
var bridgePayloads embed.FS

var loadBridges = sync.OnceValues(func() (ociimage.Bridges, error) {
	payloads, err := fs.Sub(bridgePayloads, "bridge-payloads")
	if err != nil {
		return nil, err
	}
	return ociimage.LoadBridges(payloads)
})

func prepareStdioImage(ctx context.Context, image, token string, options buildOptions) (string, error) {
	named, err := reference.ParseNamed(image)
	if err != nil {
		return "", err
	}
	repository, err := imageRepository(named.Name(), token, options.Env, options.HTTPClient)
	if err != nil {
		return "", err
	}
	descriptor, err := repository.Resolve(ctx, named.(reference.Digested).Digest().String())
	if err != nil {
		return "", err
	}
	bridges, err := loadBridges()
	if err != nil {
		return "", err
	}
	prepared, err := ociimage.AdaptImage(ctx, repository, descriptor, ociimage.AdaptOptions{
		Stdio: true, Origin: options.origin,
		Source: image, CheckedLayers: options.checkedLayers, Bridges: bridges,
	})
	if err != nil {
		return "", err
	}
	return reference.TrimNamed(named).String() + "@" + prepared.Digest.String(), nil
}
