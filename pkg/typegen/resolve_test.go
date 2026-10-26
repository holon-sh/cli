package typegen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
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
	server := mcp.NewServer(&mcp.Implementation{Name: name, Version: "99"}, nil)
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

func TestRegistrySourceResolution(t *testing.T) {
	identity := mcpcontract.Identity{Name: "example.com/orders", Version: "1.0.0"}
	live := liveServer(t, "orders-live")
	defer live.Close()
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	var record, document string
	mux.HandleFunc("/contract", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(document))
	})
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
	types := `,"_meta":{"` + PublisherMetadata + `":{"sh.holon":{"types":"` + server.URL + `/contract"}}}`
	oci := `,"packages":[{"registryType":"oci","identifier":"images.example/orders:1.0.0"}]`
	valid := string(mustJSON(t, ordersMetadata()))
	wrongIdentity := ordersMetadata()
	wrongIdentity.Identity.Name = "example.com/other"
	for _, test := range []struct {
		name, record, document, tool, want string
		image                              *http.Client
	}{
		{"types document before OCI and remote", base + types + oci + remote + `}`, valid, "lookup", "", imageClient(t, wrongIdentity, mcpcontract.MediaType)},
		{"oci package", base + oci + `}`, "", "lookup", "", nil},
		{"oci before remote", base + oci + remote + `}`, "", "lookup", "", nil},
		{"oci without a contract falls through to remote", base + oci + remote + `}`, "", "read", "", imageClient(t, nil, mcpcontract.MediaType)},
		{"oci identity mismatch", base + oci + `}`, "", "", "belongs to example.com/other", imageClient(t, wrongIdentity, mcpcontract.MediaType)},
		{"oci error propagates", base + `,"packages":[{"registryType":"oci","identifier":"orders:1.0.0"},{"registryType":"oci","identifier":"images.example/orders:1.0.0"}]` + remote + `}`, "", "", "package orders:1.0.0: package identifier must use an explicit OCI registry", nil},
		{"invalid OCI contract does not fall through", base + oci + remote + `}`, "", "", "invalid MCP contract artifact", imageClient(t, ordersMetadata(), "invalid")},
		{"remote", base + remote + `}`, "", "read", "", nil},
		{"neither", base + `,"packages":[{"registryType":"npm","identifier":"orders"}]}`, "", "", "publishes neither", nil},
		{"wrong registry identity", `{"name":"example.com/wrong","version":"1.0.0","description":"Wrong"}`, "", "", "different server name", nil},
		{"oversized document", base + types + `}`, strings.Repeat(" ", MaxDescriptorBytes+1), "", "exceeds", nil},
		{"invalid contract", base + types + oci + remote + `}`, `{"version":1,"identity":{"name":"example.com/orders","version":"1.0.0"},"contract":{"tools":"invalid"}}`, "", "expected a sequence", nil},
		{"wrong contract identity", base + types + `}`, `{"version":1,"identity":{"name":"example.com/other","version":"1.0.0"},"contract":{"tools":[]}}`, "", "belongs to example.com/other", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			record, document = test.record, test.document
			image := test.image
			if image == nil {
				image = imageClient(t, ordersMetadata(), mcpcontract.MediaType)
			}
			client := &http.Client{Transport: hostTransport{
				strings.TrimPrefix(server.URL, "https://"): server.Client().Transport,
				strings.TrimPrefix(live.URL, "http://"):    http.DefaultTransport,
				"images.example":                           image.Transport,
			}}
			ref := Reference{Name: identity.Name, Version: identity.Version, Registry: server.URL}
			metadata, err := Resolve(t.Context(), Source{Registry: &ref}, Options{HTTPClient: client, Env: []string{"DOCKER_CONFIG=" + t.TempDir()}})
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("Resolve = %v, want %q", err, test.want)
				}
				return
			}
			if err != nil || metadata.Identity != identity || len(metadata.Contract.Tools) != 1 || metadata.Contract.Tools[0].Name != test.tool {
				t.Fatalf("Resolve = %+v, %v", metadata, err)
			}
		})
	}
}

func TestContractRedirectRejectsDowngrade(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/contract", http.StatusFound)
	}))
	defer server.Close()
	if _, err := fetchContract(t.Context(), server.URL, Options{HTTPClient: server.Client()}); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("insecure redirect accepted: %v", err)
	}
}

func TestEndpointSourcesAreNamedByTheServer(t *testing.T) {
	live := liveServer(t, "Orders Live")
	defer live.Close()
	metadata, err := Resolve(t.Context(), Source{Endpoint: live.URL}, Options{HTTPClient: live.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Identity != (mcpcontract.Identity{Name: "Orders Live", Version: "99"}) || len(metadata.Contract.Tools) != 1 {
		t.Fatalf("live contract = %+v", metadata)
	}
	outputs := map[string]string{}
	for _, resource := range metadata.Contract.Resources {
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
	if _, ok := prepared.Metadata()["app"].Requires["orders-live"]; !ok {
		t.Fatalf("alias not derived from serverInfo: %+v", prepared.Metadata()["app"].Requires)
	}

	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="example"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer unauthorized.Close()
	if _, err := Resolve(t.Context(), Source{Endpoint: unauthorized.URL}, Options{HTTPClient: unauthorized.Client()}); err == nil || !strings.Contains(err.Error(), unauthorized.URL) {
		t.Fatalf("auth-protected endpoint = %v", err)
	}
}
