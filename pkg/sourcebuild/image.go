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
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/holon-sh/cli/pkg/ociimage"
)

// Assemble mounts the layers of the exact development image that built frozen,
// appends the frozen app layers, and pushes the launchable OCI manifest.
func Assemble(
	ctx context.Context, repository, development *remote.Repository, frozen digest.Digest, start []string, labels map[string]string,
) (ocispec.Descriptor, error) {
	_, data, err := oras.FetchBytes(ctx, repository, frozen.String(), oras.FetchBytesOptions{MaxBytes: 4 << 20})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	var frozenManifest ocispec.Manifest
	if err := json.Unmarshal(data, &frozenManifest); err != nil {
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
	platform := ocispec.Platform{OS: frozenConfig.OS, Architecture: frozenConfig.Architecture}
	child, err := oras.Resolve(ctx, development, named.Reference, oras.ResolveOptions{TargetPlatform: &platform})
	if errors.Is(err, errdef.ErrNotFound) {
		if _, lookupErr := development.Resolve(ctx, named.Reference); lookupErr == nil {
			return ocispec.Descriptor{}, fmt.Errorf("the development image has no %s/%s image", platform.OS, platform.Architecture)
		}
		return ocispec.Descriptor{}, missing
	}
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	assembled, err := ociimage.Edit(ctx, development, repository, child, func(image *ociimage.Image) error {
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

func configureImage(image *ociimage.Image, directory string, start []string, labels map[string]string) {
	production := []string{"HOME=/tmp", "NODE_ENV=production", "PYTHONUNBUFFERED=1"}
	environment := slices.DeleteFunc(image.Config.Env, func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return slices.Contains([]string{"HOME", "NODE_ENV", "PYTHONUNBUFFERED"}, name)
	})
	merged := maps.Collect(maps.All(image.Config.Labels))
	maps.Copy(merged, labels)
	image.Runtime["Entrypoint"] = json.RawMessage("null")
	image.Runtime["Cmd"], _ = json.Marshal(start)
	image.Runtime["WorkingDir"], _ = json.Marshal(directory)
	image.Runtime["Env"], _ = json.Marshal(append(environment, production...))
	image.Runtime["Labels"], _ = json.Marshal(merged)
}
