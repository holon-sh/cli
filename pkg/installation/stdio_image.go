package installation

import (
	"context"
	"embed"
	"io/fs"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

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

func adaptStdio(ctx context.Context, repository *remote.Repository, image ocispec.Descriptor, origin string) (ocispec.Descriptor, error) {
	bridges, err := loadBridges()
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return ociimage.AdaptImage(ctx, repository, image, ociimage.AdaptOptions{
		Origin: origin, Source: repository.Reference.String() + "@" + image.Digest.String(), CheckedLayers: &checkedLayers, Bridges: bridges,
	})
}
