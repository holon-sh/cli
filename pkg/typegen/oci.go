package typegen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/ociimage"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// DockerCredential looks registry credentials up in the Docker config, by the host being contacted.
// DOCKER_CONFIG and HOME entries in env override this process's own.
func DockerCredential(env []string) (auth.CredentialFunc, error) {
	env, err := commandenv.Environ(env)
	if err != nil {
		return nil, err
	}
	configDir := commandenv.Value(env, "DOCKER_CONFIG")
	if configDir == "" {
		home := commandenv.Value(env, "HOME")
		if home == "" {
			if home, err = os.UserHomeDir(); err != nil {
				return nil, err
			}
		}
		configDir = filepath.Join(home, ".docker")
	}
	store, err := credentials.NewStore(filepath.Join(configDir, "config.json"), credentials.StoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("read source registry credentials: %w", err)
	}
	return credentials.Credential(store), nil
}

// ReadDocument reads an image index's contract annotation, then the named blob.
// The contract manifest remains in the index for copying and garbage collection;
// it is not part of the read path.
func ReadDocument(ctx context.Context, repository *remote.Repository, descriptor ocispec.Descriptor) (*Document, error) {
	if !images.IsIndexType(descriptor.MediaType) {
		return nil, nil
	}
	var index ocispec.Index
	if err := ociimage.FetchJSON(ctx, repository, descriptor, &index); err != nil {
		return nil, err
	}
	reference := index.Annotations[mcpcontract.DigestAnnotation]
	if reference == "" {
		return nil, nil
	}
	return FetchDocument(ctx, repository.Blobs(), reference)
}

func resolveTypeMetadata(ctx context.Context, image string, o Options) (*Document, error) {
	named, err := ociimage.Parse(image)
	if err != nil {
		return nil, err
	}
	repository, err := remote.NewRepository(named.Name())
	if err != nil {
		return nil, err
	}
	credential, err := DockerCredential(o.Env)
	if err != nil {
		return nil, err
	}
	repository.Client = &auth.Client{Client: o.HTTPClient, Cache: auth.NewCache(), Credential: credential}
	descriptor, err := repository.Resolve(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("resolve dependency contract for %s: %w", image, err)
	}
	return ReadDocument(ctx, repository, descriptor)
}
