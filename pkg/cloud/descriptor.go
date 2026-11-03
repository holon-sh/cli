package cloud

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/holon-sh/cli/pkg/clientutil"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/protocol"
)

const publisherMetadata = mcpregistry.PublisherMetadata

type holonDescriptorMetadata = mcpregistry.HolonMetadata

type (
	serverDescriptor = mcpregistry.Descriptor
	serverTransport  = mcpregistry.Transport
	descriptorInput  = mcpregistry.DescriptorInput
	inputFields      = mcpregistry.InputFields
)

func readServerDescriptor(path string) (*serverDescriptor, error) {
	if err := regularNoSymlink(path, "server descriptor"); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := clientutil.ReadLimited(file, mcpregistry.MaxDescriptorBytes)
	if err != nil {
		return nil, err
	}
	return mcpregistry.ParseDescriptor(data)
}

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
