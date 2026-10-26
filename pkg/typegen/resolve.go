package typegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Resolve returns one source's contract document without writing anything: a path source is
// extracted from its snapshot, the others are fetched as Prepare would fetch them.
func Resolve(ctx context.Context, source Source, options Options) (*mcpcontract.Metadata, error) {
	o, err := options.defaults()
	if err != nil {
		return nil, err
	}
	if err := source.validate(); err != nil {
		return nil, err
	}
	switch {
	case source.Path != "":
		root, err := os.MkdirTemp("", "holon-types-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(root)
		if err = tooling.Unpack(filepath.Join(root, "tooling")); err != nil {
			return nil, err
		}
		prepared, err := prepareSource(ctx, root, "source", source.Path, o)
		if err != nil {
			return nil, err
		}
		if prepared.language == "go" {
			return nil, fmt.Errorf("%s: Go server contracts are not statically extracted", source.Path)
		}
		results, err := extractTypes(ctx, root, map[string][]extractionEntry{prepared.language: {{Name: "source", Path: prepared.root, Entry: prepared.entry}}}, o)
		if err != nil {
			return nil, err
		}
		return prepared.contract(results["source"])
	case source.Registry != nil:
		return resolveRegistry(ctx, *source.Registry, o)
	case source.Endpoint != "":
		return liveContract(ctx, source.Endpoint, nil, o.HTTPClient)
	default:
		return resolveTypeMetadata(ctx, source.Image, o)
	}
}

// resolveRegistry fetches a registry record and its contract: the record's sh.holon.types document,
// or else the schemas its streamable-http remote advertises. A record offering neither is an error.
func resolveRegistry(ctx context.Context, ref Reference, o Options) (*mcpcontract.Metadata, error) {
	d, err := (Client{HTTP: o.HTTPClient, Origin: ref.RegistryURL()}).Get(ctx, ref.Name, ref.Version)
	if err != nil {
		return nil, fmt.Errorf("%s@%s: %w", ref.Name, ref.Version, err)
	}
	var metadata *mcpcontract.Metadata
	switch {
	case d.Types != "":
		metadata, err = fetchContract(ctx, d.Types, o)
	default:
		remote := ""
		for _, transport := range d.Remotes {
			if transport.Type == "streamable-http" {
				remote = transport.URL
				break
			}
		}
		if remote == "" {
			return nil, fmt.Errorf("%s@%s publishes neither a sh.holon.types contract nor a streamable-http remote", ref.Name, ref.Version)
		}
		identity := d.Identity()
		metadata, err = liveContract(ctx, remote, &identity, o.HTTPClient)
	}
	if err != nil {
		return nil, fmt.Errorf("%s@%s: %w", ref.Name, ref.Version, err)
	}
	if metadata.Identity != d.Identity() {
		return nil, fmt.Errorf("%s@%s: its contract document belongs to %s@%s", ref.Name, ref.Version, metadata.Identity.Name, metadata.Identity.Version)
	}
	return metadata, nil
}

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
	data, err := fetchJSON(ctx, &client, location)
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

// liveContract initializes an MCP session and retrieves its advertised schemas. With a nil identity
// the server's own serverInfo names the contract; a registry source supplies its record's identity.
func liveContract(ctx context.Context, location string, identity *mcpcontract.Identity, client *http.Client) (*mcpcontract.Metadata, error) {
	u, err := url.Parse(location)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return nil, errors.New("MCP URL must be an HTTP(S) URL without credentials or a fragment")
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "holon-types", Version: "0.0.1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: location, HTTPClient: client}, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", location, err)
	}
	defer session.Close()
	if identity == nil {
		info := session.InitializeResult().ServerInfo
		if info == nil || info.Name == "" {
			return nil, fmt.Errorf("%s does not report a server name; give the client an alias", location)
		}
		identity = &mcpcontract.Identity{Name: info.Name, Version: info.Version}
	}
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
	metadata := &mcpcontract.Metadata{Version: mcpcontract.Version, Identity: *identity, Contract: contract}
	if _, err := mcptype.Canonical(ctx, map[string]any{"metadata": metadata}); err != nil {
		return nil, err
	}
	return metadata, nil
}
