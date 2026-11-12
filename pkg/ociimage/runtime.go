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
	mcpbridge "github.com/holon-sh/mcp-bridge/http"
	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
)

type bridgePayload struct {
	layer          []byte
	diffID, binary digest.Digest
}

type Bridges map[string]bridgePayload

func LoadBridges(payloads fs.FS) (Bridges, error) {
	bridges := Bridges{}
	for _, architecture := range []string{"amd64", "arm64"} {
		binary, err := fs.ReadFile(payloads, "linux-"+architecture)
		if err != nil {
			return nil, err
		}
		if bridges[architecture], err = bridgeLayer(binary); err != nil {
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
	return Rewrite(ctx, repository, descriptor, func(image *Image) error {
		return AdaptRuntime(ctx, repository, image, options)
	})
}

// AdaptRuntime starts a stdio image's entrypoint and command under mcp-bridge, which serves them over HTTP on port 3000.
func AdaptRuntime(ctx context.Context, repository oras.Target, image *Image, options AdaptOptions) error {
	if image.OS != "linux" || image.platform != nil && image.platform.Architecture != image.Architecture {
		return errors.New("stdio image platform does not match Linux image configuration")
	}
	payload, ok := options.Bridges[image.Architecture]
	if !ok {
		return fmt.Errorf("unsupported bridge architecture %q", image.Architecture)
	}
	for _, layer := range image.Layers {
		if err := checkLayer(ctx, repository, options.CheckedLayers, layer); err != nil {
			return err
		}
	}
	if image.Runtime == nil {
		return errors.New("image has no runtime configuration")
	}
	launcher := []string{
		BridgePath, "serve", "--listen", ":3000", "--protocol-version", mcpbridge.ProtocolVersion, "--allow-origin", options.Origin, "--",
	}
	if child := slices.Concat(image.Config.Entrypoint, image.Config.Cmd); len(child) == 0 || child[0] == "" {
		return errors.New("stdio image has no executable entrypoint or command")
	}
	image.Runtime["Entrypoint"], _ = json.Marshal(append(launcher, image.Config.Entrypoint...))
	image.Runtime["Cmd"], _ = json.Marshal(image.Config.Cmd)
	image.Runtime["StopSignal"] = json.RawMessage(`"SIGTERM"`)
	labels := map[string]string{}
	maps.Copy(labels, image.Config.Labels)
	labels["sh.holon.bridge.runtime"], labels["sh.holon.bridge.source"] = payload.binary.String(), options.Source
	image.Runtime["Labels"], _ = json.Marshal(labels)
	layer, err := oras.PushBytes(ctx, repository, image.layerMediaType, payload.layer)
	if err != nil {
		return err
	}
	if image.RootFS.Type != "layers" || len(image.RootFS.DiffIDs) != len(image.Layers)+len(image.appended) {
		return errors.New("image rootfs does not match its layers")
	}
	image.AppendLayer(layer, payload.diffID, "holon stdio bridge")
	return nil
}

func bridgeLayer(binary []byte) (bridgePayload, error) {
	if len(binary) == 0 {
		return bridgePayload{}, errors.New("stdio bridge is not bundled")
	}

	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	diffID := digest.Canonical.Digester()
	writer := tar.NewWriter(io.MultiWriter(zipper, diffID.Hash()))
	header := &tar.Header{Name: strings.TrimPrefix(BridgePath, "/"), Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}
	_ = writer.WriteHeader(header)
	_, _ = writer.Write(binary)
	_ = writer.Close()
	_ = zipper.Close()
	return bridgePayload{layer: compressed.Bytes(), diffID: diffID.Digest(), binary: digest.FromBytes(binary)}, nil
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
	reserved := strings.TrimPrefix(BridgePath, "/")
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
			return fmt.Errorf("image uses reserved path %s", BridgePath)
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
