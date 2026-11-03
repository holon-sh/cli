package cloud

import (
	"cmp"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/holon-sh/cli/pkg/commandenv"
	"github.com/holon-sh/cli/pkg/protocol"
)

func imageRepository(name, grant string, extraEnv []string, client *http.Client) (*remote.Repository, error) {
	repository, err := remote.NewRepository(name)
	if err != nil {
		return nil, err
	}
	authClient := &auth.Client{Client: cmp.Or(client, retry.DefaultClient), Cache: auth.NewCache()}
	repository.Client = authClient
	if grant != "" {
		authClient.Credential = auth.StaticCredential(repository.Reference.Registry,
			auth.Credential{Username: protocol.UploadUsername, Password: grant})
		return repository, nil
	}
	env, err := commandenv.Environ(extraEnv)
	if err != nil {
		return nil, err
	}
	configDir := commandenv.Value(env, "DOCKER_CONFIG")
	if configDir == "" {
		home := commandenv.Value(env, "HOME")
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
