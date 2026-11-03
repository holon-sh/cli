package host

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/holon-sh/cli/pkg/runtimecontract"
)

func TestAdmissionFailsClosed(t *testing.T) {
	valid := `{"exp":2000000060,"aud":["https://holon.example"],"principal":{"iss":"issuer","sub":"user","client_id":"client"}}`
	for _, tc := range []struct {
		name, claims string
	}{
		{"expired", strings.Replace(valid, "2000000060", "2000000000", 1)},
		{"missing expiry", `{"aud":["https://holon.example"]}`},
		{"multiple audiences", `{"exp":2000000060,"aud":["https://one.example","https://two.example"]}`},
		{"relative audience", `{"exp":2000000060,"aud":["/relative"]}`},
		{"invalid principal", strings.Replace(valid, `"client_id":"client"`, `"client_id":null`, 1)},
		{"invalid scopes", strings.Replace(valid, `"client_id":"client"`, `"client_id":"client","scope":[]`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := New(`{"version":1,"peers":{}}`, true)
			if err != nil {
				t.Fatal(err)
			}
			h.now = func() time.Time { return time.Unix(2000000000, 0) }
			h.application = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid request reached application") })
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
			r.Header.Set(runtimecontract.ContextHeader, "header."+base64.RawURLEncoding.EncodeToString([]byte(tc.claims))+".signature")
			r.Header.Set(runtimecontract.CallerHeader, "spiffe://edge")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, r)
			if response.Code != http.StatusForbidden || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("admission: %d %v", response.Code, response.Header())
			}
		})
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{
		`{"version":2,"peers":{}}`, `{"version":1}`, `{"version":1,"peers":{},"unknown":true}`,
		`{"version":1,"peers":{"peer":"https://peer/mcp"}}`, `{"version":1,"peers":{"peer":"http://user@peer/mcp"}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := New(raw, true); err == nil {
				t.Fatalf("configuration accepted: %v", err)
			}
		})
	}
}

// Context-free discovery is admitted only from the app's own installation's control plane.
func TestDiscoveryCallersFollowControlNamespace(t *testing.T) {
	for _, tc := range []struct {
		name, config, caller string
		admitted             bool
	}{
		{"default namespace", `{"version":1,"peers":{}}`, "spiffe://cluster.local/ns/holon-system/sa/holon-api", true},
		{"configured namespace", `{"version":1,"peers":{},"controlNamespace":"t-acme"}`, "spiffe://cluster.local/ns/t-acme/sa/holon-authz", true},
		{"another installation", `{"version":1,"peers":{},"controlNamespace":"t-acme"}`, "spiffe://cluster.local/ns/holon-system/sa/holon-api", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := New(tc.config, true)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodHead, "/", nil)
			r.Header.Set(runtimecontract.CallerHeader, tc.caller)
			if _, admitted := admit(r, time.Now(), h.callers); admitted != tc.admitted {
				t.Fatalf("admitted = %t", admitted)
			}
		})
	}
}
