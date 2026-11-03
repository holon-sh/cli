package cloud

import (
	"context"
	"embed"
	"io/fs"
	"strings"
	"sync"

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

var checkedLayers sync.Map

func prepareStdioImage(ctx context.Context, image, token string, options buildOptions) (string, error) {
	name, _, _ := strings.Cut(image, "@")
	repository, err := imageRepository(name, token, options.HTTPClient)
	if err != nil {
		return "", err
	}
	descriptor, err := repository.Resolve(ctx, image)
	if err != nil {
		return "", err
	}
	bridges, err := loadBridges()
	if err != nil {
		return "", err
	}
	prepared, err := ociimage.AdaptImage(ctx, repository, descriptor, ociimage.AdaptOptions{
		Stdio: true, Origin: options.origin,
		Source: image, CheckedLayers: &checkedLayers, Bridges: bridges,
	})
	if err != nil {
		return "", err
	}
	return name + "@" + prepared.Digest.String(), nil
}
