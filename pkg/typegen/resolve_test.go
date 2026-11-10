package typegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/protocol"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type hostTransport map[string]http.RoundTripper

func (h hostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	transport, ok := h[r.URL.Host]
	if !ok {
		return nil, fmt.Errorf("unexpected request host: %s", r.URL.Host)
	}
	return transport.RoundTrip(r)
}

func liveServer(t *testing.T, name string) *httptest.Server {
	t.Helper()
	return versionedServer(t, name, "99", true)
}

func versionedServer(t *testing.T, name, version string, events bool) *httptest.Server {
	t.Helper()
	var options *mcp.ServerOptions
	if events {
		options = &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}}
		options.Capabilities.AddExtension(eventsExtension, nil)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, options)
	if options != nil {
		pages := map[string]*listEventsResult{
			"":  {Events: []listedEvent{{Name: "order.shipped", Input: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`), Payload: json.RawMessage(`{"type":"string"}`)}}, NextCursor: "2"},
			"2": {Events: []listedEvent{{Name: "order.opened", Input: json.RawMessage(`{"type":"object"}`), Payload: json.RawMessage(`{}`)}}},
		}
		if err := mcp.AddReceivingCustomMethod(server, "events/list", func(_ context.Context, _ *mcp.ServerSession, params *listEventsParams) (*listEventsResult, error) {
			return pages[params.Cursor], nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)
	server.AddTool(&mcp.Tool{Name: "read", InputSchema: schema, OutputSchema: schema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Error("contract discovery executed a tool")
		return &mcp.CallToolResult{}, nil
	})
	for _, resource := range []*mcp.Resource{{URI: "orders://typed", Name: "typed", Meta: mcp.Meta{"sh.holon/outputSchema": map[string]any{"type": "string"}}}, {URI: "orders://untyped", Name: "untyped"}} {
		server.AddResource(resource, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			t.Error("contract discovery read a resource")
			return &mcp.ReadResourceResult{}, nil
		})
	}
	return httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
}

// liveServerBehind serves a live MCP server to requests whose Authorization header check accepts.
func liveServerBehind(t *testing.T, check func(authorization string) bool) *httptest.Server {
	t.Helper()
	live := liveServer(t, "orders")
	t.Cleanup(live.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch authorization := r.Header.Get("Authorization"); {
		case check(authorization):
		case r.Header.Get("Mcp-Method") == "":
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		case authorization == "":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		default:
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		live.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func tokenServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	return liveServerBehind(t, func(authorization string) bool { return authorization == "Bearer "+token })
}

// tokenlessServer fails the test if a request reaches it with a token.
func tokenlessServer(t *testing.T) *httptest.Server {
	t.Helper()
	return liveServerBehind(t, func(authorization string) bool {
		if authorization != "" {
			t.Errorf("received %q", authorization)
		}
		return true
	})
}

func TestEndpointToken(t *testing.T) {
	server := tokenServer(t, "good-value")
	for _, test := range []struct {
		name, alias, env, signedIn, want string
		signInErr                        error
	}{
		{"refused", "my-docs", "MY_DOCS_MCP_TOKEN=bad-value", "good-value", "HTTP 403; the token came from MY_DOCS_MCP_TOKEN", nil},
		{"missing", "docs", "OTHER_MCP_TOKEN=good-value", "", "HTTP 401; if it needs a token, set DOCS_MCP_TOKEN", nil},
		{"no alias to name it", "", "DOCS_MCP_TOKEN=good-value", "", "HTTP 401; if it needs a token, write ALIAS=URL and set ALIAS_MCP_TOKEN", nil},
		{"signed in", "", "", "good-value", "", nil},
		{"signed in and refused", "", "", "bad-value", "HTTP 403; the token came from signing in", nil},
		{"signed out", "", "", "", "not signed in", errors.New("not signed in")},
	} {
		t.Run(test.name, func(t *testing.T) {
			signedIn := false
			signIn := func(_ context.Context, origin string) (string, error) {
				if signedIn || origin != server.URL {
					t.Errorf("signed in again, or to %s rather than the endpoint's origin %s", origin, server.URL)
				}
				signedIn = true
				return test.signedIn, test.signInErr
			}
			document, err := ResolveDocument(t.Context(), Source{Alias: test.alias, Endpoint: server.URL}, Options{Env: []string{test.env}, SignIn: signIn})
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "-value") {
					t.Fatalf("ResolveDocument = %v, want %q without the token", err, test.want)
				}
				return
			}
			if err != nil || len(document.Schema.Tools) != 1 {
				t.Fatalf("ResolveDocument = %+v, %v", document, err)
			}
		})
	}
}

func TestEndpointTokenStaysWithItsOrigin(t *testing.T) {
	other := tokenlessServer(t)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("the named origin received %q", r.Header.Get("Authorization"))
		}
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()
	// The process environment is where the CLI's token comes from.
	t.Setenv("DOCS_MCP_TOKEN", "secret")
	if _, err := ResolveDocument(t.Context(), Source{Alias: "docs", Endpoint: redirecting.URL}, Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrySourceResolution(t *testing.T) {
	identity := mcpcontract.Identity{Name: "example.com/orders", Version: "1.0.0"}
	// A registry record's remote gets no token, even under an alias whose variable is set.
	live := tokenlessServer(t)
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	var record string
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v0.1/servers/example.com%2Forders/versions/1.0.0" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server":` + record + `}`))
	})
	base := `{"name":"example.com/orders","version":"1.0.0","description":"Orders"`
	remote := `,"remotes":[{"type":"streamable-http","url":"` + live.URL + `"}]`
	oci := `,"packages":[{"registryType":"oci","identifier":"images.example/orders:1.0.0"}]`
	wrongIdentity := ordersMetadata()
	wrongIdentity.Identity.Name = "example.com/other"
	for _, test := range []struct {
		name, record, tool, want string
		image                    *http.Client
	}{
		{"oci before remote", base + oci + remote + `}`, "lookup", "", nil},
		{"oci without a contract falls through to remote", base + oci + remote + `}`, "read", "", imageClient(t, nil)},
		{"oci identity mismatch", base + oci + `}`, "", "belongs to example.com/other", imageClient(t, wrongIdentity)},
		{"oci error propagates", base + `,"packages":[{"registryType":"oci","identifier":"orders:1.0.0"},{"registryType":"oci","identifier":"images.example/orders:1.0.0"}]` + remote + `}`, "", "package orders:1.0.0: package identifier must use an explicit OCI registry", nil},
		{"neither", base + `,"packages":[{"registryType":"npm","identifier":"orders"}]}`, "", "publishes neither", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			record = test.record
			image := test.image
			if image == nil {
				image = imageClient(t, ordersMetadata())
			}
			client := &http.Client{Transport: hostTransport{
				strings.TrimPrefix(server.URL, "https://"): server.Client().Transport,
				strings.TrimPrefix(live.URL, "http://"):    http.DefaultTransport,
				"images.example":                           image.Transport,
			}}
			ref := protocol.RegistryRef{Name: identity.Name, Version: identity.Version, Registry: server.URL}
			metadata, err := ResolveDocument(t.Context(), Source{Alias: "orders", Registry: &ref}, Options{HTTPClient: client, Env: []string{"DOCKER_CONFIG=" + t.TempDir(), "ORDERS_MCP_TOKEN=secret"}})
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("ResolveDocument = %v, want %q", err, test.want)
				}
				return
			}
			if err != nil || metadata.Identity != identity || len(metadata.Schema.Tools) != 1 || metadata.Schema.Tools[0].Name != test.tool {
				t.Fatalf("ResolveDocument = %+v, %v", metadata, err)
			}
		})
	}
}

func TestEndpointSourcesAreNamedByTheServer(t *testing.T) {
	live := liveServer(t, "Orders Live")
	defer live.Close()
	metadata, err := ResolveDocument(t.Context(), Source{Endpoint: live.URL}, Options{HTTPClient: live.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Identity != (mcpcontract.Identity{Name: "Orders Live", Version: "99"}) {
		t.Fatalf("live contract = %+v", metadata)
	}
	bareServer := versionedServer(t, "bare", "", false)
	defer bareServer.Close()
	bare, err := ResolveDocument(t.Context(), Source{Endpoint: bareServer.URL}, Options{HTTPClient: bareServer.Client()})
	if err != nil || bare.Identity != (mcpcontract.Identity{Name: "bare", Version: "0.0.0"}) {
		t.Fatalf("unversioned contract = %+v, %v", bare, err)
	}
	outputs := map[string]string{}
	for _, resource := range metadata.Schema.Resources {
		outputs[resource.URI] = string(resource.Output)
	}
	if outputs["orders://typed"] != `{"type":"string"}` || outputs["orders://untyped"] != `{}` {
		t.Fatalf("resource schemas: %v", outputs)
	}
	events := mustJSON(t, metadata.Schema.Events)
	if want := `[{"name":"order.shipped","inputSchema":{"properties":{"id":{"type":"string"}},"type":"object"},"payloadSchema":{"type":"string"}},{"name":"order.opened","inputSchema":{"type":"object"},"payloadSchema":{}}]`; string(events) != want || bare.Schema.Events != nil {
		t.Fatalf("events = %s, a server without them %v", events, bare.Schema.Events)
	}
	// As a consumer source the alias derives from that name.
	c := Consumer{Name: "app", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: []Source{{Endpoint: live.URL}}}
	prepared, err := Prepare(t.Context(), []Consumer{c}, Options{HTTPClient: live.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if reference := prepared.Metadata()["app"].Requires["orders-live"]; prepared.Documents()[reference] == nil {
		t.Fatalf("alias not derived from serverInfo: %+v", prepared.Metadata()["app"].Requires)
	}
}
