package mcpregistry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/protocol"
)

func Fetch(ctx context.Context, httpClient *http.Client, ref protocol.RegistryRef) (*Descriptor, error) {
	client := http.Client{Timeout: 30 * time.Second}
	if httpClient != nil {
		client = *httpClient
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many MCP registry redirects")
		}
		return protocol.ValidateRegistry(req.URL.String())
	}
	address := ref.RegistryURL() + "/v0.1/servers/" + url.PathEscape(ref.Name) + "/versions/" + url.PathEscape(ref.Version)
	data, err := clientutil.Get(ctx, &client, address, MaxDescriptorBytes)
	if err != nil {
		return nil, err
	}
	var record struct {
		Server json.RawMessage `json:"server"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	descriptor, err := ParseDescriptor(record.Server)
	if err != nil {
		return nil, err
	}
	if descriptor.Identity() != (mcpcontract.Identity{Name: ref.Name, Version: ref.Version}) {
		return nil, errors.New("MCP registry returned a different server name or version")
	}
	return descriptor, nil
}
