package typegen

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/distribution/reference"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/errcode"
	"oras.land/oras-go/v2/registry/remote/retry"
)

const (
	dockerConfigEnv = "DOCKER_CONFIG"
	homeEnv         = "HOME"
)

func fetchImageJSON(ctx context.Context, source content.Fetcher, descriptor ocispec.Descriptor, value any) error {
	if descriptor.Size <= 0 || descriptor.Size > 4<<20 {
		return errors.New("invalid image metadata size")
	}
	data, err := content.FetchAll(ctx, source, descriptor)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// imageRepository authenticates with supplied, then Docker-stored, credentials. The returned
// function reports whether supplied returned a credential for the repository's registry.
func imageRepository(name string, extraEnv []string, client *http.Client, supplied auth.CredentialFunc) (*remote.Repository, func() bool, error) {
	var used atomic.Bool
	repository, err := remote.NewRepository(name)
	if err != nil {
		return nil, used.Load, err
	}
	authClient := &auth.Client{Client: cmp.Or(client, retry.DefaultClient), Cache: auth.NewCache()}
	repository.Client = authClient
	if err := validateEnv(extraEnv); err != nil {
		return nil, used.Load, err
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
				return nil, used.Load, err
			}
		}
		configDir = filepath.Join(home, ".docker")
	}
	store, err := credentials.NewStore(filepath.Join(configDir, "config.json"), credentials.StoreOptions{})
	if err != nil {
		return nil, used.Load, fmt.Errorf("read source registry credentials: %w", err)
	}
	stored := credentials.Credential(store)
	authClient.Credential = stored
	if supplied != nil {
		authClient.Credential = func(ctx context.Context, host string) (auth.Credential, error) {
			credential, err := supplied(ctx, host)
			if err != nil || credential != auth.EmptyCredential {
				used.Store(true)
				return credential, err
			}
			return stored(ctx, host)
		}
	}
	return repository, used.Load, nil
}

func readTypeMetadata(ctx context.Context, source content.Fetcher, descriptor ocispec.Descriptor) (*mcpcontract.Metadata, error) {
	if descriptor.MediaType != ocispec.MediaTypeImageIndex && descriptor.MediaType != "application/vnd.docker.distribution.manifest.list.v2+json" {
		return nil, nil
	}
	var index ocispec.Index
	if err := fetchImageJSON(ctx, source, descriptor, &index); err != nil {
		return nil, err
	}
	var result *mcpcontract.Metadata
	for _, child := range index.Manifests {
		if child.ArtifactType != mcpcontract.MediaType {
			continue
		}
		if result != nil {
			return nil, errors.New("image contains multiple MCP contracts")
		}
		var manifest ocispec.Manifest
		if err := fetchImageJSON(ctx, source, child, &manifest); err != nil {
			return nil, err
		}
		if manifest.ArtifactType != mcpcontract.MediaType || len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != mcpcontract.MediaType {
			return nil, errors.New("invalid MCP contract artifact")
		}
		var metadata mcpcontract.Metadata
		if err := fetchImageJSON(ctx, source, manifest.Layers[0], &metadata); err != nil {
			return nil, err
		}
		if _, err := mcptype.Canonical(ctx, map[string]any{"metadata": metadata}); err != nil {
			return nil, err
		}
		result = &metadata
	}
	return result, nil
}

func parseImportedImage(image string) (reference.Named, error) {
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

func resolveTypeMetadata(ctx context.Context, image string, o Options) (*mcpcontract.Metadata, error) {
	named, err := parseImportedImage(image)
	if err != nil {
		return nil, err
	}
	repository, supplied, err := imageRepository(named.Name(), o.Env, o.HTTPClient, o.Credential)
	if err != nil {
		return nil, err
	}
	rejected := func(err error) error {
		var response *errcode.ErrorResponse
		if supplied() && errors.As(err, &response) && response.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("%s rejected the supplied registry credential: %w", reference.Domain(named), err)
		}
		return err
	}
	descriptor, err := repository.Resolve(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("resolve dependency contract for %s: %w", image, rejected(err))
	}
	metadata, err := readTypeMetadata(ctx, repository, descriptor)
	return metadata, rejected(err)
}
