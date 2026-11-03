package cloud

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/protocol"
)

type (
	serverDescriptor = mcpregistry.Descriptor
	serverTransport  = mcpregistry.Transport
	descriptorInput  = mcpregistry.DescriptorInput
	inputFields      = mcpregistry.InputFields
)

func fetchServerDescriptor(ctx context.Context, ref serverReference) (*serverDescriptor, error) {
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many MCP registry redirects")
		}
		return protocol.ValidateRegistry(req.URL.String())
	}}
	record, err := (mcpregistry.Client{HTTP: client, Origin: ref.RegistryURL()}).Get(ctx, ref.Name, ref.Version)
	if err != nil {
		return nil, err
	}
	return mcpregistry.ParseDescriptor(record.Server)
}
