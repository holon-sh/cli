// Package sourcebuild prepares source for a development build and assembles the result for production.
package sourcebuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/holon-sh/cli/pkg/ociimage"
)

// Assemble mounts the layers of the exact development image that built frozen,
// appends the frozen app layers, and pushes the launchable OCI manifest.
func Assemble(
	ctx context.Context, repository, development *remote.Repository, frozen digest.Digest, start []string, labels map[string]string,
) (ocispec.Descriptor, error) {
	var frozenManifest ocispec.Manifest
	if err := fetchReference(ctx, repository, frozen.String(), &frozenManifest); err != nil {
		return ocispec.Descriptor{}, err
	}
	base, err := digest.Parse(frozenManifest.Annotations[ocispec.AnnotationBaseImageDigest])
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("the image the build pushed has no %s annotation naming its base image", ocispec.AnnotationBaseImageDigest)
	}
	var frozenConfig ocispec.Image
	if err := ociimage.FetchJSON(ctx, repository, frozenManifest.Config, &frozenConfig); err != nil {
		return ocispec.Descriptor{}, err
	}
	if len(frozenManifest.Layers) != len(frozenConfig.RootFS.DiffIDs) {
		return ocispec.Descriptor{}, errors.New("the frozen image's layers do not match its configuration")
	}
	named := development.Reference
	named.Reference = base.String()
	missing := fmt.Errorf("the registry does not hold the development image %s yet; it is copied there after each upgrade, so deploy again in a minute", named)
	var index ocispec.Index
	err = fetchReference(ctx, development, named.Reference, &index)
	if errors.Is(err, errdef.ErrNotFound) {
		return ocispec.Descriptor{}, missing
	}
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	i := slices.IndexFunc(index.Manifests, func(child ocispec.Descriptor) bool {
		return child.Platform != nil && child.Platform.OS == frozenConfig.OS && child.Platform.Architecture == frozenConfig.Architecture
	})
	if i < 0 {
		return ocispec.Descriptor{}, fmt.Errorf("the development image has no %s/%s image", frozenConfig.OS, frozenConfig.Architecture)
	}
	child := index.Manifests[i]
	data, err := content.FetchAll(ctx, development, child)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	assembled, err := ociimage.Edit(ctx, development, repository, child.MediaType, data, func(image *ociimage.Image) error {
		mounts, mountCtx := errgroup.WithContext(ctx)
		for _, layer := range image.Layers {
			mounts.Go(func() error {
				return repository.Mount(mountCtx, layer, development.Reference.Repository, func() (io.ReadCloser, error) { return nil, missing })
			})
		}
		configureImage(image, frozenConfig.Config.WorkingDir, start, labels)
		for i, layer := range frozenManifest.Layers {
			image.AppendLayer(layer, frozenConfig.RootFS.DiffIDs[i], "holon deploy")
		}
		return mounts.Wait()
	})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	assembled.Platform = child.Platform
	return assembled, nil
}

func fetchReference(ctx context.Context, target oras.ReadOnlyTarget, reference string, value any) error {
	_, data, err := oras.FetchBytes(ctx, target, reference, oras.FetchBytesOptions{MaxBytes: 4 << 20})
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func configureImage(image *ociimage.Image, directory string, start []string, labels map[string]string) {
	production := []string{"HOME=/tmp", "NODE_ENV=production", "PYTHONUNBUFFERED=1"}
	environment := slices.DeleteFunc(slices.Clone(image.Config.Env), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return slices.ContainsFunc(production, func(variable string) bool { return strings.HasPrefix(variable, name+"=") })
	})
	merged := map[string]string{}
	maps.Copy(merged, image.Config.Labels)
	maps.Copy(merged, labels)
	for key, value := range map[string]any{
		"Entrypoint": nil, "Cmd": start, "WorkingDir": directory,
		"Env": append(environment, production...), "Labels": merged,
	} {
		image.Runtime[key], _ = json.Marshal(value)
	}
}

// PushIndex publishes a runnable OCI index from the assembled image and any companion manifests.
func PushIndex(ctx context.Context, target oras.Target, manifests []ocispec.Descriptor, annotations map[string]string) (ocispec.Descriptor, error) {
	data, _ := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests, Annotations: annotations,
	})
	return oras.PushBytes(ctx, target, ocispec.MediaTypeImageIndex, data)
}
