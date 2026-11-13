package typegen

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/protocol/runtimecontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Generated clients send files, so the contract lists the tools a server shows only to clients that do
// (https://github.com/modelcontextprotocol/experimental-ext-file-uploads/blob/main/specification/draft/file-uploads.md#backward-compatibility).
var clientExtensions = map[string]any{"io.modelcontextprotocol/files": map[string]any{"upload": map[string]any{}, "download": map[string]any{}}}

const eventsExtension = "io.modelcontextprotocol/events"

// Catalog is what a server advertises: its identity, capabilities, and the pages or error from each list method.
// The same result is read from language runners and passed through the development control API.
type Catalog struct {
	ServerInfo   json.RawMessage        `json:"serverInfo"`
	Capabilities json.RawMessage        `json:"capabilities"`
	Lists        map[string]CatalogList `json:"lists"`
}

type CatalogList struct {
	Pages []json.RawMessage `json:"pages"`
	Error string            `json:"error,omitempty"`
}

type listEventsParams struct {
	mcp.ParamsBase
	Cursor string `json:"cursor,omitempty"`
}

type listEventsResult struct {
	mcp.ResultBase
	Events     []mcpcontract.Event `json:"events"`
	NextCursor string              `json:"nextCursor,omitempty"`
}

func catalogMethods(capabilities *mcp.ServerCapabilities) []string {
	capabilities = cmp.Or(capabilities, &mcp.ServerCapabilities{})
	_, events := capabilities.Extensions[eventsExtension]
	declared := map[string]bool{"tools/list": capabilities.Tools != nil, "resources/list": capabilities.Resources != nil, "events/list": events}
	var methods []string
	for _, method := range runtimecontract.CatalogMethods {
		if declared[method] {
			methods = append(methods, method)
		}
	}
	return methods
}

func (c Catalog) contract() (*mcpcontract.Contract, error) {
	var capabilities mcp.ServerCapabilities
	if len(c.Capabilities) != 0 {
		if err := json.Unmarshal(c.Capabilities, &capabilities); err != nil {
			return nil, err
		}
	}
	contract := &mcpcontract.Contract{Tools: []mcpcontract.Tool{}}
	for _, method := range catalogMethods(&capabilities) {
		list := c.Lists[method]
		if list.Error != "" {
			return nil, fmt.Errorf("%s: %s", method, list.Error)
		}
		for _, data := range list.Pages {
			var page struct {
				Tools     []mcpcontract.Tool
				Resources []struct {
					URI  string
					Meta map[string]json.RawMessage `json:"_meta"`
				}
				Events []mcpcontract.Event
			}
			if err := json.Unmarshal(data, &page); err != nil {
				return nil, fmt.Errorf("%s: %w", method, err)
			}
			contract.Tools, contract.Events = append(contract.Tools, page.Tools...), append(contract.Events, page.Events...)
			for _, resource := range page.Resources {
				output, ok := resource.Meta["sh.holon/outputSchema"]
				if !ok {
					output = json.RawMessage(`{}`)
				}
				contract.Resources = append(contract.Resources, mcpcontract.Resource{URI: resource.URI, Output: output})
			}
		}
	}
	slices.SortFunc(contract.Tools, func(a, b mcpcontract.Tool) int { return cmp.Compare(a.Name, b.Name) })
	slices.SortFunc(contract.Resources, func(a, b mcpcontract.Resource) int { return cmp.Compare(a.URI, b.URI) })
	slices.SortFunc(contract.Events, func(a, b mcpcontract.Event) int { return cmp.Compare(a.Name, b.Name) })
	return contract, nil
}

func sessionPages(ctx context.Context, session *mcp.ClientSession, method string) ([]json.RawMessage, error) {
	var pages []json.RawMessage
	for cursor := ""; ; {
		var result any
		var err error
		switch method {
		case "tools/list":
			result, err = session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		case "resources/list":
			result, err = session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		default:
			result, err = mcp.CallCustomMethod[*listEventsParams, *listEventsResult](ctx, session, method, &listEventsParams{Cursor: cursor})
		}
		if err != nil {
			return nil, err
		}
		page, _ := json.Marshal(result)
		var next struct{ NextCursor string }
		_ = json.Unmarshal(page, &next)
		if pages = append(pages, page); next.NextCursor == "" {
			return pages, nil
		}
		cursor = next.NextCursor
	}
}

// ServedContract lists the server at endpoint without generating a contract document.
// Its own context ends with ctx: go-sdk takes the protocol version of an MCP request it serves from the
// context, which a listing made inside a tool call would otherwise send.
func ServedContract(ctx context.Context, endpoint string, client *http.Client) (Catalog, error) {
	own, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer context.AfterFunc(ctx, cancel)()
	session, err := dial(own, endpoint, client)
	if err != nil {
		return Catalog{}, err
	}
	defer func() { _ = session.Close() }()
	initialized := session.InitializeResult()
	info, _ := json.Marshal(initialized.ServerInfo)
	capabilities, _ := json.Marshal(initialized.Capabilities)
	served := Catalog{ServerInfo: info, Capabilities: capabilities, Lists: map[string]CatalogList{}}
	for _, method := range catalogMethods(initialized.Capabilities) {
		pages, err := sessionPages(own, session, method)
		if err != nil {
			return Catalog{}, fmt.Errorf("%s: %w", method, err)
		}
		served.Lists[method] = CatalogList{Pages: pages}
	}
	return served, nil
}
