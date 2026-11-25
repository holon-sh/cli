package typegen

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/protocol/runtimecontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Generated clients send files, so the contract lists the tools a server shows only to clients that do
// (https://github.com/modelcontextprotocol/experimental-ext-file-uploads/blob/main/specification/draft/file-uploads.md#backward-compatibility).
var clientExtensions = map[string]any{"io.modelcontextprotocol/files": map[string]any{}}

const eventsExtension = "io.modelcontextprotocol/events"

type listEventsParams struct {
	mcp.ParamsBase
	Cursor string `json:"cursor,omitempty"`
}

type listEventsResult struct {
	mcp.ResultBase
	Events     []mcpcontract.Event `json:"events"`
	NextCursor string              `json:"nextCursor,omitempty"`
}

func listCatalog(capabilities *mcp.ServerCapabilities, pages func(method string) ([]json.RawMessage, error)) (*mcpcontract.Contract, error) {
	capabilities = cmp.Or(capabilities, &mcp.ServerCapabilities{})
	_, events := capabilities.Extensions[eventsExtension]
	declared := map[string]bool{"tools/list": capabilities.Tools != nil, "resources/list": capabilities.Resources != nil, "events/list": events}
	contract := &mcpcontract.Contract{Tools: []mcpcontract.Tool{}}
	for _, method := range runtimecontract.CatalogMethods {
		if !declared[method] {
			continue
		}
		listed, err := pages(method)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		for _, data := range listed {
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

func paged(list func(cursor string) (any, error)) ([]json.RawMessage, error) {
	var pages []json.RawMessage
	for cursor := ""; ; {
		result, err := list(cursor)
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

func sessionPages(ctx context.Context, session *mcp.ClientSession) func(method string) ([]json.RawMessage, error) {
	return func(method string) ([]json.RawMessage, error) {
		return paged(func(cursor string) (any, error) {
			switch method {
			case "tools/list":
				return session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
			case "resources/list":
				return session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
			}
			return mcp.CallCustomMethod[*listEventsParams, *listEventsResult](ctx, session, method, &listEventsParams{Cursor: cursor})
		})
	}
}

// ServedContract lists what the server at endpoint serves: its serverInfo, capabilities and every page of
// each catalog it declares.
func ServedContract(ctx context.Context, endpoint string, client *http.Client) (*Served, error) {
	served, _, err := listServer(ctx, endpoint, client)
	return served, err
}

// Document is the contract s describes, under name.
func (s *Served) Document(name string) (*Document, error) {
	return s.identified(mcpcontract.Identity{Name: name, Version: unversioned})
}

// advertised is the contract s describes, under the name and version its serverInfo reports. MCP allows an empty
// serverInfo version, which SDKs report for a server that does not set one.
func (s *Served) advertised() (*Document, error) {
	var info struct{ Name, Version string }
	_ = json.Unmarshal(s.ServerInfo, &info)
	if info.Name == "" {
		return nil, errors.New("the server does not report its name")
	}
	return s.identified(mcpcontract.Identity{Name: info.Name, Version: cmp.Or(info.Version, unversioned)})
}

func (s *Served) identified(identity mcpcontract.Identity) (*Document, error) {
	contract, err := listCatalog(s.Capabilities, s.pages)
	if err != nil {
		return nil, err
	}
	return NewDocument(&mcpcontract.Metadata{Identity: identity, Schema: contract})
}

// listServer lists what the server at endpoint serves, and the contract that makes. It talks to the server under a
// context of its own that ends with ctx: go-sdk takes the protocol version of an MCP request it serves from the
// context, which a listing made inside a tool call would otherwise send.
func listServer(ctx context.Context, endpoint string, client *http.Client) (*Served, *mcpcontract.Contract, error) {
	own, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer context.AfterFunc(ctx, cancel)()
	session, err := dial(own, endpoint, client)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = session.Close() }()
	initialized := session.InitializeResult()
	info, _ := json.Marshal(initialized.ServerInfo)
	served := &Served{ServerInfo: info, Capabilities: initialized.Capabilities, Lists: map[string]ServedList{}}
	pages := sessionPages(own, session)
	contract, err := listCatalog(initialized.Capabilities, func(method string) ([]json.RawMessage, error) {
		list, err := pages(method)
		served.Lists[method] = ServedList{Pages: list}
		return list, err
	})
	return served, contract, err
}
