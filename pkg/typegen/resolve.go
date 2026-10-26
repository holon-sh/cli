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
	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Resolve returns one source's contract document without writing anything: a path source is
// extracted from its snapshot, a registry, endpoint or image source is fetched, and a supplied
// contract is validated and returned as given.
func Resolve(ctx context.Context, source Source, options Options) (*mcpcontract.Metadata, error) {
	document, err := ResolveDocument(ctx, source, options)
	if err != nil || document == nil {
		return nil, err
	}
	return document.Metadata, nil
}

// ResolveDocument resolves a source while retaining the original blob bytes.
func ResolveDocument(ctx context.Context, source Source, options Options) (*Document, error) {
	o := options.defaults()
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
		if err := prepared.extract(results["source"]); err != nil {
			return nil, err
		}
		return NewDocument(ctx, prepared.metadata)
	case source.Registry != nil:
		return resolveRegistry(ctx, *source.Registry, o)
	case source.Endpoint != "":
		return liveContract(ctx, source.Endpoint, nil, o.HTTPClient)
	case source.Image != "":
		return resolveTypeMetadata(ctx, source.Image, o)
	case source.Document != nil:
		return ParseDocument(ctx, source.Document.Data)
	default:
		return NewDocument(ctx, source.Contract)
	}
}

// resolveRegistry fetches a registry record, then its first OCI package's
// contract document or its first streamable-http remote's schemas.
// A package error is an error; a package without a contract artifact falls through to the remote.
func resolveRegistry(ctx context.Context, ref Reference, o Options) (*Document, error) {
	d, err := (Client{HTTP: o.HTTPClient, Origin: ref.RegistryURL()}).Get(ctx, ref.Name, ref.Version)
	if err != nil {
		return nil, fmt.Errorf("%s@%s: %w", ref.Name, ref.Version, err)
	}
	var metadata *Document
	for _, pkg := range d.Packages {
		if pkg.RegistryType == "oci" {
			if metadata, err = resolveTypeMetadata(ctx, pkg.Identifier, o); err != nil {
				err = fmt.Errorf("package %s: %w", pkg.Identifier, err)
			}
			break
		}
	}
	if err == nil && metadata == nil {
		for _, transport := range d.Remotes {
			if transport.Type == "streamable-http" {
				identity := d.Identity()
				metadata, err = liveContract(ctx, transport.URL, &identity, o.HTTPClient)
				break
			}
		}
	}
	if err == nil && metadata == nil {
		return nil, fmt.Errorf("%s@%s publishes neither an OCI package with a contract nor a streamable-http remote", ref.Name, ref.Version)
	}
	if err != nil {
		return nil, fmt.Errorf("%s@%s: %w", ref.Name, ref.Version, err)
	}
	if metadata.Identity != d.Identity() {
		return nil, fmt.Errorf("%s@%s: its contract document belongs to %s@%s", ref.Name, ref.Version, metadata.Identity.Name, metadata.Identity.Version)
	}
	return metadata, nil
}

// liveContract initializes an MCP session and retrieves its advertised schemas. With a nil identity
// the server's own serverInfo names the contract; a registry source supplies its record's identity.
func liveContract(ctx context.Context, location string, identity *mcpcontract.Identity, client *http.Client) (*Document, error) {
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
	metadata := &mcpcontract.Metadata{Identity: *identity, Schema: contract}
	return NewDocument(ctx, metadata)
}
