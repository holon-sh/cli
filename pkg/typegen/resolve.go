package typegen

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/holon-sh/protocol"
)

// ResolveDocument returns one source's contract document without writing anything: a path source
// is extracted by running its server, a registry or endpoint source is fetched, and a supplied
// document is validated and returned with its bytes.
func ResolveDocument(ctx context.Context, source Source, options Options) (*Document, error) {
	o, err := source.prepare(options)
	if err != nil {
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
	endpoint, err := url.Parse(source.Endpoint)
	if err != nil {
		return o.HTTPClient // liveContract reports the invalid URL.
	}
	transport := &endpointTransport{RoundTripper: cmp.Or(o.HTTPClient.Transport, http.DefaultTransport), origin: endpoint.Scheme + "://" + endpoint.Host, signIn: o.SignIn}
	if source.Alias != "" {
		transport.variable = strings.TrimSuffix(protocol.DependencyEnvName(source.Alias), "_URL") + "_TOKEN"
		transport.token = cmp.Or(commandenv.Value(o.Env, transport.variable), os.Getenv(transport.variable))
	}
	client := *o.HTTPClient
	client.Transport = transport
	return &client
}

type endpointTransport struct {
	http.RoundTripper
	origin   string
	variable string
	signIn   SignIn

	mu      sync.Mutex
	token   string
	refusal error
}

func (t *endpointTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	token, refusal := t.token, t.refusal
	t.mu.Unlock()
	if refusal != nil {
		return nil, refusal
	}
	response, err := t.send(request, token)
	if err != nil || !refused(response) {
		return response, err
	}
	from := t.variable
	if token == "" && t.signIn != nil {
		if token, err = t.signIn(request.Context(), t.origin); err != nil {
			return nil, t.refuse(err)
		}
		if token != "" {
			from = "signing in"
			t.mu.Lock()
			t.token = token
			t.mu.Unlock()
			retry := request.Clone(request.Context())
			if request.GetBody != nil {
				if retry.Body, err = request.GetBody(); err != nil {
					return nil, err
				}
			}
			if response, err = t.send(retry, token); err != nil || !refused(response) {
				return response, err
			}
		}
	}
	hint := "if it needs a token, write ALIAS=URL and set ALIAS_MCP_TOKEN"
	switch {
	case token != "":
		hint = "the token came from " + from
	case t.variable != "":
		hint = "if it needs a token, set " + t.variable
	}
	return nil, t.refuse(fmt.Errorf("server refused the request with HTTP %d; %s", response.StatusCode, hint))
}

func (t *endpointTransport) refuse(err error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refusal = err
	return err
}

func (t *endpointTransport) send(request *http.Request, token string) (*http.Response, error) {
	response, err := t.RoundTripper.RoundTrip(clientutil.Bearer(request, t.origin, token))
	if err == nil && refused(response) {
		_ = response.Body.Close()
	}
	return response, err
}

func refused(response *http.Response) bool {
	return response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden
}

// resolveRegistry fetches a registry record, then its first OCI package's
// contract document or its first streamable-http remote's schemas.
// A package error is an error; a package without a contract artifact falls through to the remote.
func resolveRegistry(ctx context.Context, ref protocol.RegistryRef, o Options) (*Document, error) {
	label := ref.Name + "@" + ref.Version
	d, err := mcpregistry.Fetch(ctx, o.HTTPClient, ref)
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
	if remote := d.StreamableRemote(); remote != "" {
		metadata, err := liveContract(ctx, remote, &identity, o.HTTPClient)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		return metadata, nil
	}
	return nil, fmt.Errorf("%s publishes neither an OCI package with a contract nor a streamable-http remote", label)
}

// liveContract initializes an MCP session and retrieves its advertised schemas. With a nil identity
// the server's own serverInfo names the contract; a registry source supplies its record's identity.
// MCP allows an empty serverInfo version, which SDKs report for a server that does not set one.
func liveContract(ctx context.Context, location string, identity *mcpcontract.Identity, client *http.Client) (*Document, error) {
	session, err := dial(ctx, location, client)
	if err != nil {
		return nil, err
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
	contract, err := listCatalog(ctx, session)
	if err != nil {
		return nil, err
	}
	return NewDocument(ctx, &mcpcontract.Metadata{Identity: *identity, Schema: contract})
}
