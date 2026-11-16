package typegen

import (
	"context"
	"fmt"
	"net/http"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Connect opens an MCP session with a source's running server: an endpoint, or a registry version's
// streamable-http remote. An endpoint with an Alias sends <ALIAS>_MCP_TOKEN as its bearer token, as
// generation does. The client's timeout bounds the registry fetch and the handshake; later requests,
// such as a tool call that runs for minutes, are bounded by ctx alone. The caller closes the session.
func Connect(ctx context.Context, source Source, options Options) (*mcp.ClientSession, error) {
	o := options.defaults()
	location, client := "", o.HTTPClient
	switch server := source.Server.(type) {
	case Endpoint:
		location, client = string(server), endpointClient(source.Alias, server, o)
	case Registry:
		d, err := mcpregistry.Fetch(ctx, o.HTTPClient, protocol.RegistryRef(server))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", server, err)
		}
		if location = d.StreamableRemote(); location == "" {
			return nil, fmt.Errorf("%s publishes no streamable-http remote to connect to", server)
		}
		// The remote serves whatever version is deployed there now, not necessarily this record's.
		fmt.Fprintf(o.Stderr, "%s: connecting to its remote %s\n", server, location)
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
	c := mcp.NewClient(&mcp.Implementation{Name: "holon", Version: "0.0.1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{Extensions: clientExtensions}})
	if err := mcp.AddSendingCustomMethod[*listEventsParams, *listEventsResult](c, "events/list"); err != nil {
		return nil, err
	}
	session, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: location, HTTPClient: client, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", location, err)
	}
	return session, nil
}
