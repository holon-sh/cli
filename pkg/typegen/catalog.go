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

func listCatalog(ctx context.Context, session *mcp.ClientSession, wire *transcript) (*mcpcontract.Contract, error) {
	contract := &mcpcontract.Contract{Tools: []mcpcontract.Tool{}}
	capabilities := cmp.Or(session.InitializeResult().Capabilities, &mcp.ServerCapabilities{})
	page := func(method string, result any) (json.RawMessage, error) {
		if wire != nil {
			return wire.result(method), nil
		}
		return json.Marshal(result)
	}
	for cursor, more := "", capabilities.Tools != nil; more; more = cursor != "" {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		data, err := page("tools/list", result)
		if err != nil {
			return nil, err
		}
		var tools struct{ Tools []mcpcontract.Tool }
		if err := json.Unmarshal(data, &tools); err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		contract.Tools, cursor = append(contract.Tools, tools.Tools...), result.NextCursor
	}
	for cursor, more := "", capabilities.Resources != nil; more; more = cursor != "" {
		result, err := session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("resources/list: %w", err)
		}
		data, err := page("resources/list", result)
		if err != nil {
			return nil, err
		}
		var resources struct {
			Resources []struct {
				URI  string
				Meta map[string]json.RawMessage `json:"_meta"`
			}
		}
		if err := json.Unmarshal(data, &resources); err != nil {
			return nil, fmt.Errorf("resources/list: %w", err)
		}
		for _, resource := range resources.Resources {
			output, ok := resource.Meta["sh.holon/outputSchema"]
			if !ok {
				output = json.RawMessage(`{}`)
			}
			contract.Resources = append(contract.Resources, mcpcontract.Resource{URI: resource.URI, Output: output})
		}
		cursor = result.NextCursor
	}
	slices.SortFunc(contract.Tools, func(a, b mcpcontract.Tool) int { return cmp.Compare(a.Name, b.Name) })
	slices.SortFunc(contract.Resources, func(a, b mcpcontract.Resource) int { return cmp.Compare(a.URI, b.URI) })
	return contract, nil
}

func converse(ctx context.Context, run func(stdin []byte, stdout io.Writer) error) (*extraction, error) {
	var previous *transcript
	for {
		t := &transcript{previous: previous, diverged: -1, queue: make(chan jsonrpc.Message, 16), done: make(chan struct{}), results: map[string]json.RawMessage{}}
		result, err := t.list(ctx)
		if t.diverged < 0 {
			return result, err
		}
		if previous != nil && t.diverged <= previous.diverged {
			return nil, fmt.Errorf("the server did not answer %s", t.methods[t.diverged])
		}
		var stdout bytes.Buffer
		if err := run(bytes.Join(t.messages, []byte("\n")), &stdout); err != nil {
			return nil, err
		}
		if err := t.answer(stdout.Bytes()); err != nil {
			return nil, err
		}
		previous = t
	}
}

type transcript struct {
	previous *transcript
	messages [][]byte
	methods  []string
	answers  map[string]*jsonrpc.Response
	diverged int

	mu      sync.Mutex
	results map[string]json.RawMessage
	queue   chan jsonrpc.Message
	done    chan struct{}
	once    sync.Once
}

func (t *transcript) list(ctx context.Context) (*extraction, error) {
	session, err := newClient().Connect(ctx, t, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	contract, err := listCatalog(ctx, session, t)
	if err != nil {
		return nil, err
	}
	info, err := json.Marshal(session.InitializeResult().ServerInfo)
	return &extraction{Contract: contract, ServerInfo: info}, err
}

func (t *transcript) result(method string) json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.results[method]
}

func (t *transcript) answer(output []byte) error {
	t.answers = map[string]*jsonrpc.Response{}
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
			t.answers[fmt.Sprint(response.ID.Raw())] = response
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
	request, _ := message.(*jsonrpc.Request)
	method := ""
	if request != nil {
		method = request.Method
	}
	t.methods = append(t.methods, method)
	if t.diverged < 0 && (previous == nil || index >= len(previous.messages) || !bytes.Equal(previous.messages[index], data)) {
		t.diverged = index
	}
	if request == nil || !request.IsCall() {
		return nil
	}
	var response *jsonrpc.Response
	if t.diverged < 0 {
		response = previous.answers[fmt.Sprint(request.ID.Raw())]
		if response == nil {
			t.diverged = index
		}
	}
	if response == nil {
		response = &jsonrpc.Response{ID: request.ID, Result: guess(request)}
	}
	if response.Error == nil {
		t.results[request.Method] = response.Result
	}
	select {
	case t.queue <- response:
		return nil
	case <-t.done:
		return io.EOF
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *transcript) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case message := <-t.queue:
		return message, nil
	case <-t.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *transcript) Close() error {
	t.once.Do(func() { close(t.done) })
	return nil
}

func (t *transcript) SessionID() string { return "" }

func guess(request *jsonrpc.Request) json.RawMessage {
	var params struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Meta            map[string]any `json:"_meta"`
	}
	_ = json.Unmarshal(request.Params, &params)
	version, _ := params.Meta[mcp.MetaKeyProtocolVersion].(string)
	capabilities := map[string]any{"tools": struct{}{}, "resources": struct{}{}}
	var result any
	switch request.Method {
	case "server/discover":
		result = map[string]any{"supportedVersions": []string{version}, "capabilities": capabilities}
	case "initialize":
		result = map[string]any{"protocolVersion": params.ProtocolVersion, "capabilities": capabilities, "serverInfo": mcp.Implementation{Name: "holon-contract"}}
	default:
		result = struct{}{}
	}
	data, _ := json.Marshal(result)
	return data
}
