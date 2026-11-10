package typegen

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Generated clients send files, so the contract lists the tools a server shows only to clients that do
// (https://github.com/modelcontextprotocol/experimental-ext-file-uploads/blob/main/specification/draft/file-uploads.md#backward-compatibility).
var clientExtensions = map[string]any{"io.modelcontextprotocol/files": map[string]any{"upload": map[string]any{}, "download": map[string]any{}}}

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

var catalogMethods = []string{"tools/list", "resources/list", "events/list"}

func listCatalog(capabilities *mcp.ServerCapabilities, pages func(method string) ([]json.RawMessage, error)) (*mcpcontract.Contract, error) {
	capabilities = cmp.Or(capabilities, &mcp.ServerCapabilities{})
	_, events := capabilities.Extensions[eventsExtension]
	declared := map[string]bool{"tools/list": capabilities.Tools != nil, "resources/list": capabilities.Resources != nil, "events/list": events}
	contract := &mcpcontract.Contract{Tools: []mcpcontract.Tool{}}
	for _, method := range catalogMethods {
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

func sessionPages(ctx context.Context, session *mcp.ClientSession) func(method string) ([]json.RawMessage, error) {
	return func(method string) ([]json.RawMessage, error) {
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
}
