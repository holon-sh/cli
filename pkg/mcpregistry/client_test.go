package mcpregistry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/holon-sh/protocol"
)

const descriptorJSON = `{"$schema":"https://untrusted.invalid/schema",
"name":"com.example/analytics",
"description":"Analytics tools",
"version":"1.0.0",
"remotes":[{"type":"streamable-http",
"url":"https://analytics.example.com/custom/mcp"}]}`

func registry(t *testing.T, handler http.HandlerFunc) protocol.RegistryRef {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return protocol.RegistryRef{Name: "com.example/analytics", Version: "1.0.0", Registry: server.URL}
}

func TestFetch(t *testing.T) {
	metadata := `,"_meta":{"` + PublisherMetadata + `":{"sh.holon":{"future":true,"requires":{"search":{"name":"com.example/search","version":"1.0.0","headers":[]}}}}}}`
	record := `{"server":` + strings.TrimSuffix(descriptorJSON, "}") + metadata + `}`
	if descriptor, err := Fetch(t.Context(), nil, registry(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(record))
	})); err != nil || descriptor.Requires["search"].Name != "com.example/search" {
		t.Fatalf("descriptor = %+v, %v", descriptor, err)
	}
	for _, test := range []struct {
		name, body, want string
		status           int
	}{
		{name: "wrong identity", body: strings.ReplaceAll(record, "1.0.0", "2.0.0"), status: 200, want: "different server name or version"},
		{name: "oversized descriptor", body: record + strings.Repeat(" ", MaxDescriptorBytes), status: 200, want: "limit"},
		{name: "unavailable", body: record, status: 503, want: "HTTP 503"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Fetch(t.Context(), nil, registry(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("want %q, got %v", test.want, err)
			}
		})
	}
	_, err := Fetch(t.Context(), nil, registry(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://registry.example/", http.StatusFound)
	}))
	if err == nil || !strings.Contains(err.Error(), "must be an HTTPS URL") {
		t.Fatalf("redirect off a valid registry: %v", err)
	}
}
