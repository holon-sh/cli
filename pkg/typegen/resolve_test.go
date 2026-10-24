package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type registryResponse func(*http.Request) (*http.Response, error)

func (response registryResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	return response(request)
}

func TestPrepareUsesUntypedClientsOnlyForUnavailableRegistryContracts(t *testing.T) {
	if _, err := typeLanguages["python"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
	for _, test := range []struct {
		name, body       string
		status           int
		transportFailure bool
		unavailable      bool
		noRuntime        bool
	}{
		{name: "missing registry record", status: http.StatusNotFound, unavailable: true},
		{name: "unused runtime", status: http.StatusNotFound, unavailable: true, noRuntime: true},
		{name: "registry transport unavailable", transportFailure: true, unavailable: true},
		{name: "wrong registry identity", status: http.StatusOK, body: `{"server":{"name":"example.com/wrong","version":"1.0.0","description":"Wrong identity"}}`},
		{name: "malformed registry descriptor", status: http.StatusOK, body: `{"server":{"name":"example.com/dependency","version":"1.0.0","description":123}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := t.TempDir()
			ref := Reference{Name: "example.com/dependency", Version: "1.0.0", Registry: "https://registry.example"}
			source := pythonProvider
			if !test.noRuntime {
				source = "from holon.apps import apps\n" + source
			}
			put(t, app, "server.py", source)
			put(t, app, "server.json", string(mustJSON(t, map[string]any{
				"name": "example.com/provider", "version": "1.0.0", "description": "Provider",
				"_meta": map[string]any{PublisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": map[string]Reference{"dependency": ref}}}},
			})))
			client := &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
				if test.transportFailure {
					return nil, errors.New("registry offline")
				}
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body)), Request: request}, nil
			})}
			manifest := Manifest{Apps: []App{{Name: "provider", Path: app}, {Name: "dependency", Registry: &RegistrySource{Reference: ref}, Access: json.RawMessage(`{"server":{}}`)}}}
			var diagnostics bytes.Buffer
			prepared, err := Prepare(context.Background(), manifest, Options{HTTPClient: client, Stderr: &diagnostics})
			if !test.unavailable {
				if prepared != nil {
					defer prepared.Close()
				}
				if err == nil {
					t.Fatal("invalid registry identity or descriptor became an untyped client")
				}
				if test.name == "wrong registry identity" && !errors.Is(err, errIdentityMismatch) {
					t.Fatalf("identity mismatch was not preserved: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unavailable dependency blocked generation: %v", err)
			}
			defer prepared.Close()
			if strings.Contains(diagnostics.String(), "typed dependency dependency unavailable:") == test.noRuntime {
				t.Fatalf("unavailable diagnostic does not match runtime usage: %s", &diagnostics)
			}
			if len(prepared.Metadata()["provider"].Requires) != 0 {
				t.Fatal("unavailable dependency was advertised as a typed expectation")
			}
			var module string
			for _, app := range prepared.Manifest.Apps {
				if app.Name == "provider" {
					module = app.prepared.packages["python"]["holon/apps/__init__.py"]
				}
			}
			if !strings.Contains(module, "dependency") || !strings.Contains(module, "async def call_tool(self, name: _builtins.str") {
				t.Fatalf("generated client does not expose untyped tool calls:\n%s", module)
			}
		})
	}
}

func TestHTTPSContractValidationAndIdentity(t *testing.T) {
	identity := mcpcontract.Identity{Name: "example.com/orders", Version: "1.0.0"}
	metadata := mcpcontract.Metadata{Version: 1, Identity: identity, Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{}}}
	for _, tc := range []struct {
		name, body string
		want       string
	}{
		{"valid", string(mustJSON(t, metadata)), ""},
		{"oversized", strings.Repeat(" ", MaxDescriptorBytes+1), "exceeds"},
		{"invalid-contract", `{"version":1,"identity":{"name":"example.com/orders","version":"1.0.0"},"contract":{"tools":"invalid"}}`, ""},
		{"identity", `{"version":1,"identity":{"name":"example.com/other","version":"1.0.0"},"contract":{"tools":[]}}`, "identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			result, err := resolveDescriptor(context.Background(), &Descriptor{Name: identity.Name, Version: identity.Version, Types: server.URL}, Options{HTTPClient: server.Client()})
			if tc.name == "valid" {
				if err != nil || result.Identity != identity {
					t.Fatalf("contract: %+v %v", result, err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted invalid contract document")
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
		if _, err := fetchContract(context.Background(), server.URL, Options{HTTPClient: server.Client()}); err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Fatalf("insecure redirect accepted: %v", err)
		}
	})
}

func TestLiveContractUsesSuppliedIdentityAndAdvertisedSchemas(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "unrelated serverInfo name", Version: "99"}, nil)
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
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	identity := mcpcontract.Identity{Name: "example.com/orders", Version: "1.0.0"}
	result, err := LiveContract(context.Background(), httpServer.URL, identity, httpServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if result.Identity != identity || len(result.Contract.Tools) != 1 || result.Contract.Tools[0].Name != "read" {
		t.Fatalf("unexpected live contract: %+v", result)
	}
	outputs := map[string]string{}
	for _, resource := range result.Contract.Resources {
		outputs[resource.URI] = string(resource.Output)
	}
	if outputs["orders://typed"] != `{"type":"string"}` || outputs["orders://untyped"] != `{}` {
		t.Fatalf("resource schemas: %v", outputs)
	}
}
