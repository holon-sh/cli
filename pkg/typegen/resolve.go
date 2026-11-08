package typegen

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ResolveDocument returns one source's contract document without writing anything: a path source
// is extracted by running its server, a registry or endpoint source is fetched, and a supplied
// document is validated and returned with its bytes.
func ResolveDocument(ctx context.Context, source Source, options Options) (*Document, error) {
	o := options.defaults()
	if err := source.validate(); err != nil {
		return nil, err
	}
	switch {
	case source.Path != "":
		prepared, err := prepareSource(ctx, "source", source.Path)
		if err != nil {
			return nil, err
		}
		if prepared.language == "go" {
			return nil, fmt.Errorf("%s: Go server contracts are not statically extracted", source.Path)
		}
		root, err := os.MkdirTemp("", "holon-types-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(root)
		if err := tooling.Unpack(filepath.Join(root, "tooling")); err != nil {
			return nil, err
		}
		// Printing a contract is how a committed one is regenerated, so a stale file is not an error here.
		if err := extractSources(ctx, root, []*preparedSource{prepared}, o, false); err != nil {
			return nil, err
		}
		return NewDocument(ctx, prepared.metadata)
	case source.Registry != nil:
		return resolveRegistry(ctx, *source.Registry, o)
	case source.Endpoint != "":
		return liveContract(ctx, source.Endpoint, nil, endpointClient(source, o))
	default:
		return ParseDocument(ctx, source.Document.Data)
	}
}

// endpointClient reads an endpoint source's token from <ALIAS>_MCP_TOKEN and explains a refusal.
// A registry record's remote never reaches here, so a token only goes to a URL its owner named.
func endpointClient(source Source, o Options) *http.Client {
	origin, err := url.Parse(source.Endpoint)
	if err != nil {
		return o.HTTPClient // liveContract reports the invalid URL.
	}
	transport := endpointTransport{RoundTripper: cmp.Or(o.HTTPClient.Transport, http.DefaultTransport), origin: origin}
	if source.Alias != "" {
		transport.variable = strings.ToUpper(strings.ReplaceAll(source.Alias, "-", "_")) + "_MCP_TOKEN"
		transport.token = cmp.Or(commandenv.Value(o.Env, transport.variable), os.Getenv(transport.variable))
	}
	client := *o.HTTPClient
	client.Transport = transport
	return &client
}

type endpointTransport struct {
	http.RoundTripper
	origin          *url.URL
	variable, token string
}

func (t endpointTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// Redirects come through here too, and must not carry the token to another origin.
	if t.token != "" && request.URL.Scheme == t.origin.Scheme && request.URL.Host == t.origin.Host {
		request = request.Clone(request.Context())
		request.Header.Set("Authorization", "Bearer "+t.token)
	}
	response, err := t.RoundTripper.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden {
		return response, err
	}
	_ = response.Body.Close()
	hint := "if it needs a token, write ALIAS=URL and set ALIAS_MCP_TOKEN"
	switch {
	case t.token != "":
		hint = "the token came from " + t.variable
	case t.variable != "":
		hint = "if it needs a token, set " + t.variable
	}
	return nil, fmt.Errorf("server refused the request with HTTP %d; %s", response.StatusCode, hint)
}

func validEndpoint(location string) error {
	u, err := url.Parse(location)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return errors.New("MCP URL must be an HTTP(S) URL without credentials or a fragment")
	}
	return nil
}

// resolveRegistry fetches a registry record, then its first OCI package's
// contract document or its first streamable-http remote's schemas.
// A package error is an error; a package without a contract artifact falls through to the remote.
func resolveRegistry(ctx context.Context, ref Reference, o Options) (*Document, error) {
	label := ref.Name + "@" + ref.Version
	d, err := (Client{HTTP: o.HTTPClient, Origin: ref.RegistryURL()}).Get(ctx, ref.Name, ref.Version)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	identity := d.Identity()
	for _, pkg := range d.Packages {
		if pkg.RegistryType != "oci" {
			continue
		}
		metadata, err := resolveTypeMetadata(ctx, pkg.Identifier, o)
		if err != nil {
			return nil, fmt.Errorf("%s: package %s: %w", label, pkg.Identifier, err)
		}
		if metadata == nil {
			break
		}
		if metadata.Identity != identity {
			return nil, fmt.Errorf("%s: its contract document belongs to %s@%s", label, metadata.Identity.Name, metadata.Identity.Version)
		}
		return metadata, nil
	}
	for _, transport := range d.Remotes {
		if transport.Type == "streamable-http" {
			metadata, err := liveContract(ctx, transport.URL, &identity, o.HTTPClient)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", label, err)
			}
			return metadata, nil
		}
	}
	return nil, fmt.Errorf("%s publishes neither an OCI package with a contract nor a streamable-http remote", label)
}

// liveContract initializes an MCP session and retrieves its advertised schemas. With a nil identity
// the server's own serverInfo names the contract; a registry source supplies its record's identity.
// MCP allows an empty serverInfo version, which SDKs report for a server that does not set one.
func liveContract(ctx context.Context, location string, identity *mcpcontract.Identity, client *http.Client) (*Document, error) {
	if err := validEndpoint(location); err != nil {
		return nil, err
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "holon-types", Version: "0.0.1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: location, HTTPClient: client, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", location, err)
	}
	defer session.Close()
	initialized := session.InitializeResult()
	if identity == nil {
		info := initialized.ServerInfo
		if info == nil || info.Name == "" {
			return nil, fmt.Errorf("%s does not report a server name; give the client an alias", location)
		}
		identity = &mcpcontract.Identity{Name: info.Name, Version: cmp.Or(info.Version, unversioned)}
	}
	contract := &mcpcontract.Contract{Tools: []mcpcontract.Tool{}}
	if initialized.Capabilities.Tools != nil {
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
	if initialized.Capabilities.Resources != nil {
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
	return NewDocument(ctx, &mcpcontract.Metadata{Identity: *identity, Schema: contract})
}
