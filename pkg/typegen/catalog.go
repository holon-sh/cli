package typegen

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newClient() *mcp.Client {
	capabilities := &mcp.ClientCapabilities{}
	// Generated clients send files, so the contract lists the tools a server shows only to clients that do
	// (https://github.com/modelcontextprotocol/experimental-ext-file-uploads/blob/main/specification/draft/file-uploads.md#backward-compatibility).
	capabilities.AddExtension("io.modelcontextprotocol/files", map[string]any{"upload": map[string]any{}, "download": map[string]any{}})
	client := mcp.NewClient(&mcp.Implementation{Name: "holon", Version: "0.0.1"}, &mcp.ClientOptions{Capabilities: capabilities})
	_ = mcp.AddSendingCustomMethod[*listEventsParams, *listEventsResult](client, "events/list")
	return client
}

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

func listCatalog(ctx context.Context, session *mcp.ClientSession) (*mcpcontract.Contract, error) {
	contract := &mcpcontract.Contract{Tools: []mcpcontract.Tool{}}
	capabilities := cmp.Or(session.InitializeResult().Capabilities, &mcp.ServerCapabilities{})
	if capabilities.Tools != nil {
		for tool, err := range session.Tools(ctx, nil) {
			if err != nil {
				return nil, fmt.Errorf("tools/list: %w", err)
			}
			input, _ := json.Marshal(tool.InputSchema)
			var output json.RawMessage
			if tool.OutputSchema != nil {
				output, _ = json.Marshal(tool.OutputSchema)
			}
			contract.Tools = append(contract.Tools, mcpcontract.Tool{Name: tool.Name, Input: input, Output: output})
		}
	}
	if capabilities.Resources != nil {
		for resource, err := range session.Resources(ctx, nil) {
			if err != nil {
				return nil, fmt.Errorf("resources/list: %w", err)
			}
			output := json.RawMessage(`{}`)
			if schema, ok := resource.Meta["sh.holon/outputSchema"]; ok {
				output, _ = json.Marshal(schema)
			}
			contract.Resources = append(contract.Resources, mcpcontract.Resource{URI: resource.URI, Output: output})
		}
	}
	if _, ok := capabilities.Extensions[eventsExtension]; ok {
		for params := (&listEventsParams{}); ; {
			page, err := mcp.CallCustomMethod[*listEventsParams, *listEventsResult](ctx, session, "events/list", params)
			if err != nil {
				return nil, fmt.Errorf("events/list: %w", err)
			}
			contract.Events = append(contract.Events, page.Events...)
			if params.Cursor = page.NextCursor; params.Cursor == "" {
				break
			}
		}
	}
	slices.SortFunc(contract.Tools, func(a, b mcpcontract.Tool) int { return cmp.Compare(a.Name, b.Name) })
	slices.SortFunc(contract.Resources, func(a, b mcpcontract.Resource) int { return cmp.Compare(a.URI, b.URI) })
	slices.SortFunc(contract.Events, func(a, b mcpcontract.Event) int { return cmp.Compare(a.Name, b.Name) })
	return contract, nil
}

func converse(ctx context.Context, run func(stdin []byte, stdout io.Writer) error) (*extraction, error) {
	var sent [][]byte
	answers, answered := map[jsonrpc.ID]*jsonrpc.Response{}, -1
	for {
		messages, diverged, result, err := replay(ctx, sent, answers)
		if diverged < 0 {
			return result, err
		}
		if diverged <= answered {
			message, _ := jsonrpc.DecodeMessage(messages[diverged])
			return nil, fmt.Errorf("the server did not answer %s", message.(*jsonrpc.Request).Method)
		}
		var stdout bytes.Buffer
		if err := run(bytes.Join(messages, []byte("\n")), &stdout); err != nil {
			return nil, err
		}
		answers = map[jsonrpc.ID]*jsonrpc.Response{}
		scanner := bufio.NewScanner(&stdout)
		scanner.Buffer(nil, maxContractBytes)
		for scanner.Scan() {
			message, err := jsonrpc.DecodeMessage(scanner.Bytes())
			if err != nil {
				return nil, fmt.Errorf("the server wrote something other than MCP: %w", err)
			}
			if response, ok := message.(*jsonrpc.Response); ok {
				answers[response.ID] = response
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		sent, answered = messages, diverged
	}
}

func replay(ctx context.Context, sent [][]byte, answers map[jsonrpc.ID]*jsonrpc.Response) (messages [][]byte, diverged int, result *extraction, err error) {
	client, server := mcp.NewInMemoryTransports()
	connection, err := server.Connect(ctx)
	if err != nil {
		return nil, -1, nil, err
	}
	diverged, done := -1, make(chan struct{})
	go func() {
		defer close(done)
		for {
			message, err := connection.Read(ctx)
			if err != nil {
				return
			}
			data, _ := jsonrpc.EncodeMessage(message)
			if index := len(messages); diverged < 0 && (index >= len(sent) || !bytes.Equal(sent[index], data)) {
				diverged = index
			}
			messages = append(messages, data)
			if request, ok := message.(*jsonrpc.Request); ok && request.IsCall() {
				response := answers[request.ID]
				if diverged < 0 && response == nil {
					diverged = len(messages) - 1
				}
				if diverged >= 0 {
					response = &jsonrpc.Response{ID: request.ID, Result: guess(request)}
				}
				_ = connection.Write(ctx, response)
			}
		}
	}()
	result, err = list(ctx, client)
	_ = connection.Close()
	<-done
	return messages, diverged, result, err
}

func list(ctx context.Context, transport mcp.Transport) (*extraction, error) {
	session, err := newClient().Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	contract, err := listCatalog(ctx, session)
	if err != nil {
		return nil, err
	}
	info, err := json.Marshal(session.InitializeResult().ServerInfo)
	return &extraction{Contract: contract, ServerInfo: info}, err
}

func guess(request *jsonrpc.Request) json.RawMessage {
	if request.Method != "server/discover" {
		return json.RawMessage(`{}`)
	}
	var params mcp.DiscoverParams
	_ = json.Unmarshal(request.Params, &params)
	version, _ := params.Meta[mcp.MetaKeyProtocolVersion].(string)
	capabilities := &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}, Resources: &mcp.ResourceCapabilities{}}
	capabilities.AddExtension(eventsExtension, nil)
	data, _ := json.Marshal(&mcp.DiscoverResult{SupportedVersions: []string{version}, Capabilities: capabilities})
	return data
}
