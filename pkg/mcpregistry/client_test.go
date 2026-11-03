package mcpregistry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/protocol"
)

const descriptorJSON = `{"$schema":"https://untrusted.invalid/schema",
"name":"com.example/analytics",
"description":"Analytics tools",
"version":"1.0.0",
"remotes":[{"type":"streamable-http",
"url":"https://analytics.example.com/custom/mcp"}]}`

func TestRegistryReadFailures(t *testing.T) {
	record := `{"server":` + descriptorJSON + `}`
	for _, test := range []struct {
		name, body string
		status     int
		list       bool
	}{
		{name: "wrong identity", body: strings.ReplaceAll(record, "1.0.0", "2.0.0"), status: 200},
		{name: "oversized descriptor", body: record + strings.Repeat(" ", MaxDescriptorBytes), status: 200},
		{name: "oversized page", body: `{"servers":[]}` + strings.Repeat(" ", 4<<20), status: 200, list: true},
		{name: "missing page", body: `{}`, status: 200, list: true},
		{name: "unavailable", body: record, status: 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := Client{Origin: protocol.MCPRegistryOrigin, HTTP: httpClient(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body)), Header: http.Header{}}, nil
			})}
			var err error
			if test.list {
				_, err = client.List(t.Context(), "", "", 25)
			} else {
				_, err = client.Get(t.Context(), "com.example/analytics", "1.0.0")
			}
			if err == nil {
				t.Fatal("invalid response was accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := Client{
		Origin: protocol.MCPRegistryOrigin,
		HTTP:   httpClient(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }),
	}
	if _, err := client.Get(ctx, "com.example/analytics", "1.0.0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestOfficialRegistryRedirects(t *testing.T) {
	for _, target := range []string{"https://evil.example/v0.1", "http://registry.modelcontextprotocol.io/v0.1", protocol.MCPRegistryOrigin + "/loop"} {
		t.Run(target, func(t *testing.T) {
			count := 0
			transport := httpClient(func(r *http.Request) (*http.Response, error) {
				count++
				if r.URL.Host != "registry.modelcontextprotocol.io" || r.URL.Scheme != "https" {
					t.Fatalf("request escaped origin: %s", r.URL)
				}
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			}).Transport
			_, err := OfficialClient(transport).Get(t.Context(), "com.example/analytics", "1.0.0")
			if err == nil || count > 5 {
				t.Fatalf("redirect: requests=%d, %v", count, err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func httpClient(roundTrip roundTripFunc) *http.Client { return &http.Client{Transport: roundTrip} }
