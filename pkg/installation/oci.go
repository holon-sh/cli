package installation

import (
	"cmp"
	"net/http"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/holon-sh/protocol"
)

// imageRepository authenticates with the upload grant, bound to its own registry, or else with Docker-stored credentials.
func imageRepository(name, grant string, client *http.Client) (*remote.Repository, error) {
	repository, err := remote.NewRepository(name)
	if err != nil {
		return nil, err
	}
	credential := auth.StaticCredential(repository.Reference.Registry, auth.Credential{Username: protocol.UploadUsername, Password: grant})
	if grant == "" {
		if credential, err = typegen.DockerCredential(nil); err != nil {
			return nil, err
		}
	}
	repository.Client = &auth.Client{Client: cmp.Or(client, retry.DefaultClient), Cache: auth.NewCache(), Credential: credential}
	return repository, nil
}
