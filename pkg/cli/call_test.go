package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ordersServer serves one tool that echoes its arguments as structured content, failing for id "0".
func ordersServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "example.com/orders", Version: "1.2.0"}, &mcp.ServerOptions{Instructions: "Look orders up by id."})
	schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"count":{"type":"integer"}},"required":["id"]}`)
	server.AddTool(&mcp.Tool{Name: "read", Description: "Read an order.\nMore detail.", InputSchema: schema}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var arguments map[string]any
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{StructuredContent: arguments, IsError: arguments["id"] == "0", Content: []mcp.Content{&mcp.TextContent{Text: "unused"}}}, nil
	})
	server.AddPrompt(&mcp.Prompt{Name: "summarize", Arguments: []*mcp.PromptArgument{{Name: "id", Required: true}, {Name: "tone"}}}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{}, nil
	})
	live := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(live.Close)
	return live
}

func TestListPrintsTheHandshakeAndEachListing(t *testing.T) {
	live := ordersServer(t)
	got := run(t, "", "list", live.URL)
	for _, want := range []string{"example.com/orders 1.2.0 (protocol ", "\nLook orders up by id.\n", "\nTools (1)\n  read(id, count?)\n      Read an order.\n\nPrompts (1)\n  summarize(id, tone?)\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Resources") {
		t.Errorf("list printed resources the server does not advertise:\n%s", got)
	}
}

func TestCallReadsEachValueAsJSONOrAString(t *testing.T) {
	live := ordersServer(t)
	// A dependency the working directory's server.json declares is a TARGET too.
	dir := t.TempDir()
	writeCLIFile(t, dir, "server.json", `{"name":"example.com/shop","version":"0.1.0","description":"Shop","_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{"orders":{"url":"`+live.URL+`"}}}}}}`)
	t.Chdir(dir)
	got := run(t, "", "call", "orders", "read", "id=A-1", "count=2")
	var arguments map[string]any
	if err := json.Unmarshal([]byte(got), &arguments); err != nil {
		t.Fatalf("call printed %q, not the structured content: %v", got, err)
	}
	if arguments["id"] != "A-1" || arguments["count"] != 2.0 {
		t.Errorf("the tool received %v, want id as the string \"A-1\" and count as the number 2", arguments)
	}
	if got := run(t, "", "call", live.URL, "read", `--args={"id":"9","count":1}`, "count=3"); got != "{\n  \"count\": 3,\n  \"id\": \"9\"\n}\n" {
		t.Errorf("--args with an overriding pair sent %s", got)
	}
	run(t, `has no tool "write"; it has read`, "call", live.URL, "write")
	// A string that reads as JSON is quoted.
	run(t, "read returned an error", "call", live.URL, "read", `id="0"`)
}

// guarded admits only requests bearing token; a refused initialize gets 400, as the platform's gateway answers the
// legacy initialize the SDK falls back to.
func guarded(t *testing.T, token string, next http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+token {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Mcp-Method") == "" { // Only server/discover names its method in a header.
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCallSignsInOnceTheEndpointRequiresIt(t *testing.T) {
	live := guarded(t, "signed-in", ordersServer(t).Config.Handler)
	var asked []string
	signIn := func(_ context.Context, origin string) (string, error) {
		asked = append(asked, origin)
		return "signed-in", nil
	}
	if got := runSignedIn(t, signIn, "", "call", live.URL+"/mcp/orders", "read", "id=A-1"); got != "{\n  \"id\": \"A-1\"\n}\n" {
		t.Errorf("a signed-in call printed %q", got)
	}
	if !slices.Equal(asked, []string{live.URL}) {
		t.Errorf("signed in for %q, want once for the endpoint's origin %s", asked, live.URL)
	}
	// An endpoint that admits anyone never asks.
	runSignedIn(t, func(context.Context, string) (string, error) {
		t.Error("an open endpoint asked for sign-in")
		return "", nil
	}, "", "list", ordersServer(t).URL)
}

func TestListReportsThatAnEndpointRequiresSignIn(t *testing.T) {
	live := guarded(t, "signed-in", http.NotFoundHandler())
	noCredential := func(context.Context, string) (string, error) { return "", nil }
	// Not the 400 the legacy initialize after the refused server/discover gets.
	runSignedIn(t, noCredential, "requires sign-in (HTTP 401)", "list", live.URL+"/mcp/orders")
}
