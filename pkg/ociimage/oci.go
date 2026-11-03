package ociimage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

func FetchJSON(ctx context.Context, source content.Fetcher, descriptor ocispec.Descriptor, kind string, value any) error {
	if descriptor.Size <= 0 || descriptor.Size > 4<<20 {
		return fmt.Errorf("invalid image %s size", kind)
	}
	data, err := content.FetchAll(ctx, source, descriptor)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func Parse(image string) (reference.Named, error) {
	named, err := reference.ParseNamed(image)
	if err != nil {
		return nil, errors.New("package identifier must use an explicit OCI registry")
	}
	if digested, ok := named.(reference.Digested); ok {
		if digested.Digest().Algorithm() != digest.SHA256 {
			return nil, errors.New("package image requires a SHA-256 digest")
		}
		return named, nil
	}
	if _, ok := named.(reference.Tagged); !ok {
		return nil, errors.New("package image requires an explicit tag or digest")
	}
	return named, nil
}

func SupportedPlatform(platform ocispec.Platform) bool {
	return platform.OS == "linux" && slices.Contains([]string{"amd64", "arm64"}, platform.Architecture)
}
