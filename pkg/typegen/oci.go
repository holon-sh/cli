package typegen

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/ociimage"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/retry"
)

const (
	dockerConfigEnv = "DOCKER_CONFIG"
	homeEnv         = "HOME"
)

// imageRepository authenticates with Docker-stored credentials.
func imageRepository(name string, extraEnv []string, client *http.Client) (*remote.Repository, error) {
	repository, err := remote.NewRepository(name)
	if err != nil {
		return nil, err
	}
	authClient := &auth.Client{Client: cmp.Or(client, retry.DefaultClient), Cache: auth.NewCache()}
	repository.Client = authClient
	if err := validateEnv(extraEnv); err != nil {
		return nil, err
	}
	env := map[string]string{
		dockerConfigEnv: os.Getenv(dockerConfigEnv),
		homeEnv:         os.Getenv(homeEnv),
	}
	for _, entry := range extraEnv {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	configDir := env[dockerConfigEnv]
	if configDir == "" {
		home := env[homeEnv]
		if home == "" {
			home, err = os.UserHomeDir()
			if err != nil {
				return nil, err
			}
		}
		configDir = filepath.Join(home, ".docker")
	}
	store, err := credentials.NewStore(filepath.Join(configDir, "config.json"), credentials.StoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("read source registry credentials: %w", err)
	}
	authClient.Credential = credentials.Credential(store)
	return repository, nil
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
	repository, err := imageRepository(named.Name(), o.Env, o.HTTPClient)
	if err != nil {
		return nil, err
	}
	descriptor, err := repository.Resolve(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("resolve dependency contract for %s: %w", image, err)
	}
	return ReadDocument(ctx, repository, descriptor)
}
