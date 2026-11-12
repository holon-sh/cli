package ociimage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
)

type runtimePayload struct {
	layer          []byte
	diffID, binary digest.Digest
}

type Bridges map[string]runtimePayload

func LoadBridges(payloads fs.FS) (Bridges, error) {
	bridges := Bridges{}
	for _, architecture := range []string{"amd64", "arm64"} {
		binary, err := fs.ReadFile(payloads, "linux-"+architecture)
		if err != nil {
			return nil, err
		}
		if bridges[architecture], err = runtimeLayer(binary); err != nil {
			return nil, err
		}
	}
	return bridges, nil
}

type AdaptOptions struct {
	Origin        string
	Source        string
	CheckedLayers *sync.Map
	Bridges       Bridges
}

func AdaptImage(ctx context.Context, repository oras.Target, descriptor ocispec.Descriptor, options AdaptOptions) (ocispec.Descriptor, error) {
	return adapt(ctx, repository, descriptor, nil, options)
}

func adaptManifest(
	ctx context.Context, repository oras.Target, document map[string]json.RawMessage, mediaType string, platform *ocispec.Platform, options AdaptOptions,
) error {
	var configDescriptor ocispec.Descriptor
	var layers []ocispec.Descriptor
	if err := json.Unmarshal(document["config"], &configDescriptor); err != nil {
		return err
	}
	if err := json.Unmarshal(document["layers"], &layers); err != nil {
		return err
	}
	var data json.RawMessage
	if err := FetchJSON(ctx, repository, configDescriptor, &data); err != nil {
		return err
	}
	var config, runtime map[string]json.RawMessage
	var image configuration
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &image); err != nil {
		return err
	}
	_ = json.Unmarshal(config["config"], &runtime)
	if image.OS != "linux" || platform != nil && platform.Architecture != image.Architecture {
		return errors.New("stdio image platform does not match Linux image configuration")
	}
	payload, ok := options.Bridges[image.Architecture]
	if !ok {
		return fmt.Errorf("unsupported bridge architecture %q", image.Architecture)
	}
	for _, layer := range layers {
		if err := checkLayer(ctx, repository, options.CheckedLayers, layer); err != nil {
			return err
		}
	}
	if runtime == nil {
		return errors.New("image has no runtime configuration")
	}
	launcher := []string{RuntimePath, "--origin", options.Origin}
	if child := slices.Concat(image.Config.Entrypoint, image.Config.Cmd); len(child) == 0 || child[0] == "" {
		return errors.New("stdio image has no executable entrypoint or command")
	}
	runtime["Entrypoint"], _ = json.Marshal(append(append(launcher, "--"), image.Config.Entrypoint...))
	runtime["Cmd"], _ = json.Marshal(image.Config.Cmd)
	runtime["StopSignal"] = json.RawMessage(`"SIGTERM"`)
	labels := map[string]string{}
	maps.Copy(labels, image.Config.Labels)
	labels["sh.holon.bridge.runtime"], labels["sh.holon.bridge.source"] = payload.binary.String(), options.Source
	runtime["Labels"], _ = json.Marshal(labels)
	layerMediaType := ocispec.MediaTypeImageLayerGzip
	if mediaType == images.MediaTypeDockerSchema2Manifest {
		layerMediaType = images.MediaTypeDockerSchema2LayerGzip
	}
	layer, err := oras.PushBytes(ctx, repository, layerMediaType, payload.layer)
	if err != nil {
		return err
	}
	if image.RootFS.Type != "layers" || len(image.RootFS.DiffIDs) != len(layers) {
		return errors.New("image rootfs does not match its layers")
	}
	image.RootFS.DiffIDs = append(image.RootFS.DiffIDs, payload.diffID)
	config["config"], _ = json.Marshal(runtime)
	config["rootfs"], _ = json.Marshal(image.RootFS)
	if len(image.History) != 0 {
		config["history"], _ = json.Marshal(append(image.History, ocispec.History{CreatedBy: "holon stdio runtime"}))
	}
	preparedConfig, _ := json.Marshal(config)
	newConfig, err := oras.PushBytes(ctx, repository, configDescriptor.MediaType, preparedConfig)
	if err != nil {
		return err
	}
	configDescriptor.Digest, configDescriptor.Size = newConfig.Digest, newConfig.Size
	configDescriptor.Data, configDescriptor.URLs = nil, nil
	document["config"], _ = json.Marshal(configDescriptor)
	document["layers"], _ = json.Marshal(append(layers, layer))
	return nil
}

func runtimeLayer(binary []byte) (runtimePayload, error) {
	if len(binary) == 0 {
		return runtimePayload{}, errors.New("stdio runtime is not bundled")
	}

	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	diffID := digest.Canonical.Digester()
	writer := tar.NewWriter(io.MultiWriter(zipper, diffID.Hash()))
	header := &tar.Header{Name: strings.TrimPrefix(RuntimePath, "/"), Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}
	_ = writer.WriteHeader(header)
	_, _ = writer.Write(binary)
	_ = writer.Close()
	_ = zipper.Close()
	return runtimePayload{layer: compressed.Bytes(), diffID: diffID.Digest(), binary: digest.FromBytes(binary)}, nil
}

func checkLayer(ctx context.Context, repository oras.Target, checkedLayers *sync.Map, descriptor ocispec.Descriptor) error {
	key := struct {
		Digest    digest.Digest
		Size      int64
		MediaType string
	}{descriptor.Digest, descriptor.Size, descriptor.MediaType}
	if _, ok := checkedLayers.Load(key); ok {
		return ctx.Err()
	}
	stream, err := repository.Fetch(ctx, descriptor)
	if err != nil {
		return err
	}
	defer stream.Close()
	verified := content.NewVerifyReader(stream, descriptor)
	var reader io.Reader = verified
	switch descriptor.MediaType {
	case ocispec.MediaTypeImageLayerGzip, images.MediaTypeDockerSchema2LayerGzip:
		zipper, err := gzip.NewReader(verified)
		if err != nil {
			return err
		}
		defer zipper.Close()
		reader = zipper
	case ocispec.MediaTypeImageLayerZstd:
		zipper, err := zstd.NewReader(verified, zstd.WithDecoderMaxMemory(64<<20))
		if err != nil {
			return err
		}
		defer zipper.Close()
		reader = zipper
	case ocispec.MediaTypeImageLayer:
	default:
		return fmt.Errorf("unsupported stdio image layer %q", descriptor.MediaType)
	}
	archive := tar.NewReader(reader)
	reserved := strings.TrimPrefix(RuntimePath, "/")
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(path.Clean("/"+header.Name), "/")
		if first, _, _ := strings.Cut(name, "/"); first == reserved || name == ".wh."+reserved {
			return fmt.Errorf("image uses reserved path %s", RuntimePath)
		}
	}
	if _, err := io.Copy(io.Discard, io.MultiReader(reader, verified)); err != nil {
		return err
	}
	if err := verified.Verify(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	checkedLayers.Store(key, struct{}{})
	return nil
}
