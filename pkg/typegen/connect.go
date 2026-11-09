package typegen

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Connect opens an MCP session with a source's running server: an endpoint, or a registry version's
// streamable-http remote. An endpoint with an Alias sends <ALIAS>_MCP_TOKEN as its bearer token, as
// generation does. The client's timeout bounds the registry fetch and the handshake; later requests,
// such as a tool call that runs for minutes, are bounded by ctx alone. The caller closes the session.
func Connect(ctx context.Context, source Source, options Options) (*mcp.ClientSession, error) {
	o := options.defaults()
	if err := source.validate(); err != nil {
		return nil, err
	}
	if source.from != "" {
		fmt.Fprintf(o.Stderr, "%s: using %s from %s\n", source.Alias, source.Endpoint, source.from)
	}
	location, client := source.Endpoint, o.HTTPClient
	switch {
	case source.Endpoint != "":
		client = endpointClient(source, o)
	case source.Registry != nil:
		d, err := mcpregistry.Fetch(ctx, o.HTTPClient, *source.Registry)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		if location = d.StreamableRemote(); location == "" {
			return nil, fmt.Errorf("%s publishes no streamable-http remote to connect to", source)
		}
		// The remote serves whatever version is deployed there now, not necessarily this record's.
		fmt.Fprintf(o.Stderr, "%s: connecting to its remote %s\n", source, location)
	default:
		return nil, errors.New("only an endpoint or a registry version has a running server to connect to")
	}
	session := *client
	session.Timeout = 0
	if client.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, client.Timeout)
		defer cancel()
	}
	return dial(ctx, location, &session)
}

// dial opens a session with the MCP endpoint at location. The session outlives ctx, which bounds
// only the handshake.
func dial(ctx context.Context, location string, client *http.Client) (*mcp.ClientSession, error) {
	if err := mcpregistry.ValidateEndpoint(location); err != nil {
		return nil, err
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "holon", Version: "0.0.1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: location, HTTPClient: client, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", location, err)
	}
	return session, nil
}
