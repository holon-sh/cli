package mcpregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/holon-sh/cli/pkg/clientutil"
	"github.com/holon-sh/cli/pkg/protocol"
)

func Fetch(ctx context.Context, ref protocol.RegistryRef) (*Descriptor, error) {
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many MCP registry redirects")
		}
		return protocol.ValidateRegistry(req.URL.String())
	}}
	address := ref.RegistryURL() + "/v0.1/servers/" + url.PathEscape(ref.Name) + "/versions/" + url.PathEscape(ref.Version)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP registry returned HTTP %d", response.StatusCode)
	}
	data, err := clientutil.ReadLimited(response.Body, MaxDescriptorBytes)
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
	if descriptor.Identity() != ref.Identity() {
		return nil, errors.New("MCP registry returned a different server name or version")
	}
	return descriptor, nil
}
