package typegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/url"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote/errcode"
	"time"
)

func fetchContract(ctx context.Context, location string, o Options) (*mcpcontract.Metadata, error) {
	if err := validateTypesURL(location); err != nil {
		return nil, err
	}
	client := *o.HTTPClient
	previous := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateTypesURL(req.URL.String()); err != nil {
			return err
		}
		if len(via) >= 5 {
			return errors.New("too many contract redirects")
		}
		if previous != nil {
			return previous(req, via)
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, HTTPError(response.StatusCode)
	}
	data, err := readLimited(response.Body, MaxDescriptorBytes)
	if err != nil {
		return nil, err
	}
	// Validate the original document before decoding the contract wire format.
	if _, err := mcptype.Canonical(ctx, map[string]any{"metadata": json.RawMessage(data)}); err != nil {
		return nil, err
	}
	var metadata mcpcontract.Metadata
	if err = json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

// LiveContract initializes an MCP session and retrieves its advertised schemas.
// Identity must be supplied by the caller because MCP serverInfo is not a registry identity.
func LiveContract(ctx context.Context, location string, identity mcpcontract.Identity, client *http.Client) (*mcpcontract.Metadata, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if err := (Reference{Name: identity.Name, Version: identity.Version}).Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(location)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return nil, errors.New("MCP URL must be an HTTP(S) URL without credentials or a fragment")
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "holon-types", Version: "0.1.0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: location, HTTPClient: client}, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	contract := &mcpcontract.Contract{Tools: []mcpcontract.Tool{}}
	if session.InitializeResult().Capabilities.Tools != nil {
		for tool, err := range session.Tools(ctx, nil) {
			if err != nil {
				return nil, err
			}
			input, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return nil, err
			}
			var output json.RawMessage
			if tool.OutputSchema != nil {
				output, err = json.Marshal(tool.OutputSchema)
				if err != nil {
					return nil, err
				}
			}
			contract.Tools = append(contract.Tools, mcpcontract.Tool{Name: tool.Name, Input: input, Output: output})
		}
	}
	if session.InitializeResult().Capabilities.Resources != nil {
		for resource, err := range session.Resources(ctx, nil) {
			if err != nil {
				return nil, err
			}
			output := json.RawMessage(`{}`)
			if schema, ok := resource.Meta["sh.holon/outputSchema"]; ok {
				output, err = json.Marshal(schema)
				if err != nil {
					return nil, err
				}
			}
			contract.Resources = append(contract.Resources, mcpcontract.Resource{URI: resource.URI, Output: output})
		}
	}
	metadata := &mcpcontract.Metadata{Version: mcpcontract.Version, Identity: identity, Contract: contract}
	if _, err := mcptype.Canonical(ctx, map[string]any{"metadata": metadata}); err != nil {
		return nil, err
	}
	return metadata, nil
}

func resolveRequired(ctx context.Context, name string, ref Reference, local map[string]*mcpcontract.Metadata, declared map[string]App, o Options) (*mcpcontract.Metadata, string, error) {
	if app, ok := declared[name]; ok && app.Path != "" {
		m := local[name]
		diagnostic := "source contract is not statically extracted; its clients are untyped"
		if app.prepared != nil && app.prepared.diagnostic != "" {
			diagnostic = app.prepared.diagnostic
		}
		return m, diagnostic, nil
	}
	app := declared[name]
	if app.unavailable != "" {
		return nil, app.unavailable, nil
	}
	if app.Image != "" {
		metadata, err := resolveTypeMetadata(ctx, app.Image, o)
		if err != nil {
			if ctx.Err() == nil && contractUnavailable(err) {
				return nil, err.Error(), nil
			}
			return nil, "", err
		}
		return metadata, "provider has no available typed contract; its clients are untyped", nil
	}
	descriptor := app.descriptor
	if descriptor == nil {
		var err error
		descriptor, err = (Client{HTTP: o.HTTPClient, Origin: ref.RegistryURL()}).Get(ctx, ref.Name, ref.Version)
		if err != nil {
			if ctx.Err() != nil || !contractUnavailable(err) {
				return nil, "", err
			}
			return nil, err.Error(), nil
		}
	}
	if descriptor.Identity() != ref.Identity() {
		return nil, "", fmt.Errorf("%s requires %s@%s, but %s image contract belongs to %s@%s", name, ref.Name, ref.Version, name, descriptor.Name, descriptor.Version)
	}
	metadata, err := resolveDescriptor(ctx, descriptor, o)
	if err != nil {
		if ctx.Err() == nil && contractUnavailable(err) {
			return nil, err.Error(), nil
		}
		return nil, "", err
	}
	return metadata, "provider has no available typed contract; its clients are untyped", nil
}
func resolveDescriptor(ctx context.Context, d *Descriptor, o Options) (*mcpcontract.Metadata, error) {
	var metadata *mcpcontract.Metadata
	var err error
	if d.Types != "" {
		metadata, err = fetchContract(ctx, d.Types, o)
	} else {
		for _, pkg := range d.Packages {
			if pkg.RegistryType == "oci" {
				metadata, err = resolveTypeMetadata(ctx, pkg.Identifier, o)
				break
			}
		}
		if err == nil && metadata == nil {
			for _, remote := range d.Remotes {
				if remote.Type == "streamable-http" {
					metadata, err = LiveContract(ctx, remote.URL, d.Identity(), o.HTTPClient)
					break
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if metadata != nil && metadata.Identity != d.Identity() {
		return nil, fmt.Errorf("%s descriptor identity does not match the retained image contract; use the original release identity or a new image tag", d.Name)
	}
	return metadata, nil
}

func contractUnavailable(err error) bool {
	var status HTTPError
	var transport *url.Error
	var registry *errcode.ErrorResponse
	return errors.As(err, &status) || errors.As(err, &transport) || errors.Is(err, errdef.ErrNotFound) ||
		errors.As(err, &registry) && (registry.StatusCode == 401 || registry.StatusCode == 403 || registry.StatusCode == 404)
}
