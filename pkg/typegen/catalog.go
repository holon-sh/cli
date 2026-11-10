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
	"sync"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newClient() *mcp.Client {
	capabilities := &mcp.ClientCapabilities{}
	// Generated clients send files, so the contract lists the tools a server shows only to clients that do
	// (https://github.com/modelcontextprotocol/experimental-ext-file-uploads/blob/main/specification/draft/file-uploads.md#backward-compatibility).
	capabilities.AddExtension("io.modelcontextprotocol/files", map[string]any{"upload": map[string]any{}, "download": map[string]any{}})
	return mcp.NewClient(&mcp.Implementation{Name: "holon", Version: "0.0.1"}, &mcp.ClientOptions{Capabilities: capabilities})
}

func listCatalog(ctx context.Context, session *mcp.ClientSession, raw func() json.RawMessage) (*mcpcontract.Contract, error) {
	contract := &mcpcontract.Contract{Tools: []mcpcontract.Tool{}}
	capabilities := cmp.Or(session.InitializeResult().Capabilities, &mcp.ServerCapabilities{})
	err := pages(capabilities.Tools != nil, "tools/list", raw, func(cursor string) (any, string, error) {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, "", err
		}
		return result, result.NextCursor, nil
	}, func(data json.RawMessage) error {
		var page struct{ Tools []mcpcontract.Tool }
		err := json.Unmarshal(data, &page)
		contract.Tools = append(contract.Tools, page.Tools...)
		return err
	})
	if err != nil {
		return nil, err
	}
	err = pages(capabilities.Resources != nil, "resources/list", raw, func(cursor string) (any, string, error) {
		result, err := session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return nil, "", err
		}
		return result, result.NextCursor, nil
	}, func(data json.RawMessage) error {
		var page struct {
			Resources []struct {
				URI  string
				Meta map[string]json.RawMessage `json:"_meta"`
			}
		}
		err := json.Unmarshal(data, &page)
		for _, resource := range page.Resources {
			output, ok := resource.Meta["sh.holon/outputSchema"]
			if !ok {
				output = json.RawMessage(`{}`)
			}
			contract.Resources = append(contract.Resources, mcpcontract.Resource{URI: resource.URI, Output: output})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(contract.Tools, func(a, b mcpcontract.Tool) int { return cmp.Compare(a.Name, b.Name) })
	slices.SortFunc(contract.Resources, func(a, b mcpcontract.Resource) int { return cmp.Compare(a.URI, b.URI) })
	return contract, nil
}

func pages(listed bool, method string, raw func() json.RawMessage, list func(cursor string) (any, string, error), add func(json.RawMessage) error) error {
	for cursor, more := "", listed; more; more = cursor != "" {
		result, next, err := list(cursor)
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		data, err := json.Marshal(result)
		if raw != nil {
			data = raw()
		}
		if err == nil {
			err = add(data)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		cursor = next
	}
	return nil
}

func converse(ctx context.Context, run func(stdin []byte, stdout io.Writer) error) (*extraction, error) {
	var previous *transcript
	for {
		t := &transcript{previous: previous, diverged: -1, queue: make(chan jsonrpc.Message, 16)}
		t.ctx, t.cancel = context.WithCancel(ctx)
		result, err := t.list()
		if t.diverged < 0 {
			return result, err
		}
		if previous != nil && t.diverged <= previous.diverged {
			message, _ := jsonrpc.DecodeMessage(t.messages[t.diverged])
			return nil, fmt.Errorf("the server did not answer %s", message.(*jsonrpc.Request).Method)
		}
		var stdout bytes.Buffer
		if err := run(bytes.Join(t.messages, []byte("\n")), &stdout); err != nil {
			return nil, err
		}
		if err := t.answer(stdout.Bytes()); err != nil {
			return nil, err
		}
		t.previous, previous = nil, t
	}
}

type transcript struct {
	previous *transcript
	messages [][]byte
	answers  map[jsonrpc.ID]*jsonrpc.Response
	diverged int

	mu     sync.Mutex
	last   json.RawMessage
	queue  chan jsonrpc.Message
	ctx    context.Context
	cancel context.CancelFunc
}

func (t *transcript) list() (*extraction, error) {
	session, err := newClient().Connect(t.ctx, t, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	contract, err := listCatalog(t.ctx, session, func() json.RawMessage {
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.last
	})
	if err != nil {
		return nil, err
	}
	info, err := json.Marshal(session.InitializeResult().ServerInfo)
	return &extraction{Contract: contract, ServerInfo: info}, err
}

func (t *transcript) answer(output []byte) error {
	t.answers = map[jsonrpc.ID]*jsonrpc.Response{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(nil, maxContractBytes)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		message, err := jsonrpc.DecodeMessage(scanner.Bytes())
		if err != nil {
			return fmt.Errorf("the server wrote something other than MCP: %w", err)
		}
		if response, ok := message.(*jsonrpc.Response); ok {
			t.answers[response.ID] = response
		}
	}
	return scanner.Err()
}

func (t *transcript) Connect(context.Context) (mcp.Connection, error) { return t, nil }

func (t *transcript) Write(ctx context.Context, message jsonrpc.Message) error {
	data, err := jsonrpc.EncodeMessage(message)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	index, previous := len(t.messages), t.previous
	t.messages = append(t.messages, data)
	if t.diverged < 0 && (previous == nil || index >= len(previous.messages) || !bytes.Equal(previous.messages[index], data)) {
		t.diverged = index
	}
	request, _ := message.(*jsonrpc.Request)
	if request == nil || !request.IsCall() {
		return nil
	}
	var response *jsonrpc.Response
	if t.diverged < 0 {
		if response = previous.answers[request.ID]; response == nil {
			t.diverged = index
		}
	}
	if response == nil {
		response = &jsonrpc.Response{ID: request.ID, Result: guess(request)}
	}
	t.last = response.Result
	select {
	case t.queue <- response:
		return nil
	case <-t.ctx.Done():
		return io.EOF
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *transcript) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case message := <-t.queue:
		return message, nil
	case <-t.ctx.Done():
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *transcript) Close() error {
	t.cancel()
	return nil
}

func (t *transcript) SessionID() string { return "" }

func guess(request *jsonrpc.Request) json.RawMessage {
	var params mcp.InitializeParams
	_ = json.Unmarshal(request.Params, &params)
	capabilities := &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}, Resources: &mcp.ResourceCapabilities{}}
	var result any = struct{}{}
	switch request.Method {
	case "server/discover":
		version, _ := params.Meta[mcp.MetaKeyProtocolVersion].(string)
		result = &mcp.DiscoverResult{SupportedVersions: []string{version}, Capabilities: capabilities}
	case "initialize":
		result = &mcp.InitializeResult{ProtocolVersion: params.ProtocolVersion, Capabilities: capabilities, ServerInfo: &mcp.Implementation{Name: "holon-contract"}}
	}
	data, _ := json.Marshal(result)
	return data
}
