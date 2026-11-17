package ociimage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
)

type Image struct {
	OS             string                     `json:"os"`
	Architecture   string                     `json:"architecture"`
	Config         ocispec.ImageConfig        `json:"config"`
	RootFS         ocispec.RootFS             `json:"rootfs"`
	History        []ocispec.History          `json:"history"`
	Layers         []ocispec.Descriptor       `json:"-"`
	Runtime        map[string]json.RawMessage `json:"-"`
	appended       []ocispec.Descriptor
	platform       *ocispec.Platform
	layerMediaType string
}

func (image *Image) AppendLayer(layer ocispec.Descriptor, diffID digest.Digest, createdBy string) {
	image.appended = append(image.appended, layer)
	image.RootFS.DiffIDs = append(image.RootFS.DiffIDs, diffID)
	if len(image.History) != 0 {
		image.History = append(image.History, ocispec.History{CreatedBy: createdBy})
	}
}

func Rewrite(ctx context.Context, repository oras.Target, descriptor ocispec.Descriptor, transform func(*Image) error) (ocispec.Descriptor, error) {
	return rewrite(ctx, repository, descriptor, nil, transform)
}

func rewrite(
	ctx context.Context, repository oras.Target, descriptor ocispec.Descriptor, platform *ocispec.Platform, transform func(*Image) error,
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
			if child.Platform == nil || !SupportedPlatform(*child.Platform) {
				return ocispec.Descriptor{}, errors.New("prepared images must contain only Linux amd64 or arm64 executable manifests")
			}
			prepared, err := rewrite(ctx, repository, child, child.Platform, transform)
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
		prepared, _ := json.Marshal(document)
		return oras.PushBytes(ctx, repository, descriptor.MediaType, prepared)
	case images.IsManifestType(descriptor.MediaType):
		return edit(ctx, repository, repository, descriptor.MediaType, document, platform, transform)
	default:
		return ocispec.Descriptor{}, fmt.Errorf("unsupported stdio image manifest %q", descriptor.MediaType)
	}
}

func Edit(
	ctx context.Context, source content.Fetcher, destination oras.Target, mediaType string, manifest []byte, transform func(*Image) error,
) (ocispec.Descriptor, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(manifest, &document); err != nil {
		return ocispec.Descriptor{}, err
	}
	return edit(ctx, source, destination, mediaType, document, nil, transform)
}

func edit(
	ctx context.Context, source content.Fetcher, destination oras.Target, mediaType string, document map[string]json.RawMessage,
	platform *ocispec.Platform, transform func(*Image) error,
) (ocispec.Descriptor, error) {
	var configDescriptor ocispec.Descriptor
	var layers []ocispec.Descriptor
	if err := json.Unmarshal(document["config"], &configDescriptor); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := json.Unmarshal(document["layers"], &layers); err != nil {
		return ocispec.Descriptor{}, err
	}
	var data json.RawMessage
	if err := FetchJSON(ctx, source, configDescriptor, &data); err != nil {
		return ocispec.Descriptor{}, err
	}
	var config map[string]json.RawMessage
	image := Image{Layers: layers, platform: platform, layerMediaType: ocispec.MediaTypeImageLayerGzip}
	if err := json.Unmarshal(data, &config); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := json.Unmarshal(data, &image); err != nil {
		return ocispec.Descriptor{}, err
	}
	_ = json.Unmarshal(config["config"], &image.Runtime)
	if mediaType == images.MediaTypeDockerSchema2Manifest {
		image.layerMediaType = images.MediaTypeDockerSchema2LayerGzip
	}
	if err := transform(&image); err != nil {
		return ocispec.Descriptor{}, err
	}
	config["config"], _ = json.Marshal(image.Runtime)
	config["rootfs"], _ = json.Marshal(image.RootFS)
	if len(image.History) != 0 {
		config["history"], _ = json.Marshal(image.History)
	}
	preparedConfig, _ := json.Marshal(config)
	newConfig, err := oras.PushBytes(ctx, destination, configDescriptor.MediaType, preparedConfig)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	configDescriptor.Digest, configDescriptor.Size = newConfig.Digest, newConfig.Size
	configDescriptor.Data, configDescriptor.URLs = nil, nil
	document["config"], _ = json.Marshal(configDescriptor)
	document["layers"], _ = json.Marshal(append(image.Layers, image.appended...))
	prepared, _ := json.Marshal(document)
	return oras.PushBytes(ctx, destination, mediaType, prepared)
}
