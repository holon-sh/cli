package typegen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registry serves one record for example.com/orders@1.0.0 whose server document is body.
func registry(t *testing.T, server string) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v0.1/servers/example.com%2Forders/versions/1.0.0" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server":` + server + `}`))
	}))
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

func TestRegistrySourcesUseTheContractDocumentThenTheRemote(t *testing.T) {
	identity := mcpcontract.Identity{Name: "example.com/orders", Version: "1.0.0"}
	document := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(mustJSON(t, mcpcontract.Metadata{Version: 1, Identity: identity, Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{lookupTool}}}))
	}))
	defer document.Close()
	live := liveServer(t, "orders-live")
	defer live.Close()
	record := func(extra string) string {
		return `{"name":"example.com/orders","version":"1.0.0","description":"Orders"` + extra + `}`
	}
	for _, test := range []struct{ name, server, tool, want string }{
		{"types document", record(`,"_meta":{"` + PublisherMetadata + `":{"sh.holon":{"types":"` + document.URL + `"}}},"remotes":[{"type":"streamable-http","url":"` + live.URL + `"}]`), "lookup", ""},
		{"remote", record(`,"remotes":[{"type":"streamable-http","url":"` + live.URL + `"}]`), "read", ""},
		{"neither", record(`,"packages":[{"registryType":"oci","identifier":"images.example/orders:1.0.0"}]`), "", "publishes neither a sh.holon.types contract nor a streamable-http remote"},
		{"wrong identity", `{"name":"example.com/wrong","version":"1.0.0","description":"Wrong"}`, "", "different server name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := registry(t, test.server)
			defer server.Close()
			registryTransport, documentTransport := server.Client().Transport, document.Client().Transport
			client := &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
				switch request.URL.Host {
				case document.Listener.Addr().String():
					return documentTransport.RoundTrip(request)
				case live.Listener.Addr().String():
					return http.DefaultTransport.RoundTrip(request)
				}
				return registryTransport.RoundTrip(request)
			})}
			ref := Reference{Name: identity.Name, Version: identity.Version, Registry: server.URL}
			metadata, err := Resolve(t.Context(), Source{Registry: &ref}, Options{HTTPClient: client})
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

func TestContractDocumentValidation(t *testing.T) {
	identity := mcpcontract.Identity{Name: "example.com/orders", Version: "1.0.0"}
	for _, tc := range []struct{ name, body, want string }{
		{"oversized", strings.Repeat(" ", MaxDescriptorBytes+1), "exceeds"},
		{"invalid-contract", `{"version":1,"identity":{"name":"example.com/orders","version":"1.0.0"},"contract":{"tools":"invalid"}}`, ""},
		{"identity", `{"version":1,"identity":{"name":"example.com/other","version":"1.0.0"},"contract":{"tools":[]}}`, "belongs to example.com/other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer document.Close()
			server := registry(t, `{"name":"example.com/orders","version":"1.0.0","description":"Orders","_meta":{"`+PublisherMetadata+`":{"sh.holon":{"types":"`+document.URL+`"}}}}`)
			defer server.Close()
			registryTransport, documentTransport := server.Client().Transport, document.Client().Transport
			client := &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == document.Listener.Addr().String() {
					return documentTransport.RoundTrip(request)
				}
				return registryTransport.RoundTrip(request)
			})}
			_, err := resolveRegistry(t.Context(), Reference{Name: identity.Name, Version: identity.Version, Registry: server.URL}, Options{HTTPClient: client})
			if err == nil {
				t.Fatal("accepted an invalid contract document")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	t.Run("redirect downgrade", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://example.com/contract", http.StatusFound)
		}))
		defer server.Close()
		if _, err := fetchContract(t.Context(), server.URL, Options{HTTPClient: server.Client()}); err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Fatalf("insecure redirect accepted: %v", err)
		}
	})
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
