package ociimage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

type configuration struct {
	OS           string              `json:"os"`
	Architecture string              `json:"architecture"`
	Config       ocispec.ImageConfig `json:"config"`
	RootFS       ocispec.RootFS      `json:"rootfs"`
	History      []ocispec.History   `json:"history"`
}

func adapt(
	ctx context.Context, repository oras.Target, descriptor ocispec.Descriptor, platform *ocispec.Platform, options AdaptOptions,
) (ocispec.Descriptor, error) {
	var document map[string]json.RawMessage
	if err := FetchJSON(ctx, repository, descriptor, &document); err != nil {
		return ocispec.Descriptor{}, err
	}
	switch {
	case images.IsIndexType(descriptor.MediaType):
		if platform != nil {
			return ocispec.Descriptor{}, errors.New("nested image indexes are unsupported")
		}
		var children []ocispec.Descriptor
		if err := json.Unmarshal(document["manifests"], &children); err != nil {
			return ocispec.Descriptor{}, errors.New("image index has no manifests")
		}
		manifests := make([]ocispec.Descriptor, 0, len(children))
		executable := false
		for _, child := range children {
			if child.ArtifactType == mcpcontract.MediaType {
				manifests = append(manifests, child)
				continue
			}
			if child.ArtifactType != "" || child.Annotations["vnd.docker.reference.type"] == "attestation-manifest" {
				continue
			}
			if child.Platform == nil || !supportedPlatform(*child.Platform) {
				return ocispec.Descriptor{}, errors.New("prepared images must contain only Linux amd64 or arm64 executable manifests")
			}
			prepared, err := adapt(ctx, repository, child, child.Platform, options)
			if err != nil {
				return ocispec.Descriptor{}, err
			}
			child.Digest, child.Size = prepared.Digest, prepared.Size
			child.Data, child.URLs = nil, nil
			manifests = append(manifests, child)
			executable = true
		}
		if !executable {
			return ocispec.Descriptor{}, errors.New("image index has no executable manifests")
		}
		document["manifests"], _ = json.Marshal(manifests)
	case images.IsManifestType(descriptor.MediaType):
		if err := adaptManifest(ctx, repository, document, descriptor.MediaType, platform, options); err != nil {
			return ocispec.Descriptor{}, err
		}
	default:
		return ocispec.Descriptor{}, fmt.Errorf("unsupported stdio image manifest %q", descriptor.MediaType)
	}
	prepared, _ := json.Marshal(document)
	return oras.PushBytes(ctx, repository, descriptor.MediaType, prepared)
}
