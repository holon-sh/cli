package typegen

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	return versionedServer(t, name, "99")
}

func versionedServer(t *testing.T, name, version string) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, nil)
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
		if !check(r.Header.Get("Authorization")) {
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
		name, alias, env, want string
	}{
		{"hyphenated alias", "my-docs", "MY_DOCS_MCP_TOKEN=good-value", ""},
		{"refused", "docs", "DOCS_MCP_TOKEN=bad-value", "HTTP 403; the token came from DOCS_MCP_TOKEN"},
		{"missing", "docs", "OTHER_MCP_TOKEN=good-value", "HTTP 403; if it needs a token, set DOCS_MCP_TOKEN"},
		{"no alias to name it", "", "DOCS_MCP_TOKEN=good-value", "write ALIAS=URL and set ALIAS_MCP_TOKEN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document, err := ResolveDocument(t.Context(), Source{Alias: test.alias, Endpoint: server.URL}, Options{Env: []string{test.env}})
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

func TestEndpointTokenIsNotWritten(t *testing.T) {
	const token = "written-secret"
	server := tokenServer(t, token)
	app := t.TempDir()
	slotPackage(t, app, "holon-sh")
	c := Consumer{Name: "app", Dir: app, Targets: []string{TargetTS, TargetGo}, Sources: []Source{{Alias: "docs", Endpoint: server.URL}}}
	if err := Generate(t.Context(), []Consumer{c}, Options{Env: []string{"DOCS_MCP_TOKEN=" + token}}); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(app, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), token) {
			t.Errorf("%s reveals the token", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
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
		{"wrong registry identity", `{"name":"example.com/wrong","version":"1.0.0","description":"Wrong"}`, "", "different server name", nil},
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
	bareServer := versionedServer(t, "bare", "")
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
