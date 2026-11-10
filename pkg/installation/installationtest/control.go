package installationtest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	bridgeproxy "github.com/holon-sh/mcp-bridge/proxy"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/holon-sh/protocol"
)

type controlFixture struct {
	tools   []string
	match   map[string]string
	handler http.HandlerFunc
}

type ControlFixtures struct{ entries []controlFixture }

func NewControlFixtures(t *testing.T, mux *http.ServeMux) *ControlFixtures {
	t.Helper()
	fixtures := &ControlFixtures{}
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid MCP", http.StatusBadRequest)
			return
		}
		if request.Method == "server/discover" {
			WriteJSON(w, map[string]any{
				"jsonrpc": "2.0", "id": request.ID,
				"result": mcp.DiscoverResult{
					SupportedVersions: []string{bridgeproxy.CurrentVersion},
					Capabilities:      &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
				},
			})
			return
		}
		if request.Method == "notifications/cancelled" && len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if request.Method != "tools/call" {
			t.Errorf("unexpected MCP method %s", request.Method)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Mcp-Name") != request.Params.Name || r.Header.Get("Mcp-Protocol-Version") != bridgeproxy.CurrentVersion {
			t.Error("missing MCP routing metadata")
		}
		var arguments map[string]any
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			t.Error(err)
			return
		}
		for _, fixture := range fixtures.entries {
			matches := slices.Contains(fixture.tools, request.Params.Name)
			for key, value := range fixture.match {
				matches = matches && arguments[key] == value
			}
			if !matches {
				continue
			}
			r.Body = io.NopCloser(bytes.NewReader(request.Params.Arguments))
			recorder := &controlRecorder{ResponseRecorder: httptest.NewRecorder(), writer: w}
			fixture.handler(recorder, r)
			if recorder.hijacked {
				return
			}
			if recorder.Code == 401 || recorder.Code == 503 {
				w.WriteHeader(recorder.Code)
				_, _ = w.Write(recorder.Body.Bytes())
				return
			}
			if recorder.Code >= 400 {
				code := map[int]string{
					400: "invalid_request", 403: "forbidden", 404: "not_found", 409: "conflict", 412: "precondition_failed",
					428: "precondition_required",
				}[recorder.Code]
				if code == "" {
					t.Fatalf("unexpected fixture status %d", recorder.Code)
				}
				output := protocol.ErrorResponse{Error: protocol.Error{Code: code, Message: http.StatusText(recorder.Code)}}
				WriteJSON(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": mcp.CallToolResult{IsError: true, StructuredContent: output}})
				return
			}
			var output any
			if err := json.Unmarshal(recorder.Body.Bytes(), &output); err != nil {
				t.Errorf("invalid tool fixture output: %s", recorder.Body)
				http.Error(w, "fixture", http.StatusInternalServerError)
				return
			}
			WriteJSON(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": mcp.CallToolResult{
				StructuredContent: output,
				Content:           []mcp.Content{&mcp.TextContent{Text: recorder.Body.String()}},
			}})
			return
		}
		t.Errorf("unexpected tool %s: %s", request.Params.Name, request.Params.Arguments)
		http.NotFound(w, r)
	})
	return fixtures
}

func (f *ControlFixtures) Handle(tools []string, match map[string]string, handler http.HandlerFunc) {
	f.entries = append(f.entries, controlFixture{tools: tools, match: match, handler: handler})
}

type controlRecorder struct {
	*httptest.ResponseRecorder

	writer   http.ResponseWriter
	hijacked bool
}

func (w *controlRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return w.writer.(http.Hijacker).Hijack()
}

func ToolName(r *http.Request) string { return r.Header.Get("Mcp-Name") }

func ToolArguments(r *http.Request) map[string]any {
	data, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(data))
	var input map[string]any
	_ = json.Unmarshal(data, &input)
	return input
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func HTTPClient(roundTrip func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: roundTripFunc(roundTrip)}
}

func OriginClient(server *httptest.Server) *http.Client {
	base := server.Client()
	transport := base.Transport
	client := HTTPClient(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.Host = request.URL.Host
		clone.URL.Host = server.Listener.Addr().String()
		return transport.RoundTrip(clone)
	})
	client.Timeout = base.Timeout
	return client
}

func AssertOrigin(t *testing.T, request *http.Request, expected string) {
	t.Helper()
	if request.Host != expected {
		t.Errorf("request host = %q, want %q", request.Host, expected)
	}
}

func SessionResponse(resource, endpoint string) map[string]any {
	return map[string]any{
		"name":       resource,
		"endpoints":  []protocol.SessionEndpoint{{Name: "alpha", URL: endpoint}},
		"conditions": []map[string]any{{"type": "Ready", "status": "True", "reason": "Ready", "message": ""}},
		"revision":   "1",
	}
}

func MachineAuthServer(t *testing.T, tokenHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc(protocol.APIPathInfo, func(w http.ResponseWriter, r *http.Request) {
		AssertOrigin(t, r, strings.TrimPrefix(server.URL, "https://"))
		WriteJSON(w, map[string]any{
			"installation": protocol.InstallationReference{Origin: server.URL, UID: "test-installation"},
			"version":      "test", "oauth": map[string]any{
				"issuer": "https://identity.example/realm", "adminAudience": AdminAudience(server.URL), "cliClientID": "human-cli",
			},
		})
	})
	mux.HandleFunc("/realm/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		AssertOrigin(t, r, "identity.example")
		WriteJSON(w, map[string]any{"issuer": "https://identity.example/realm", "token_endpoint": "https://identity.example/token"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		AssertOrigin(t, r, "identity.example")
		id, secret, _ := r.BasicAuth()
		if id != "ci-deployer" || secret != "ci-secret" {
			t.Error("incorrect machine token request")
		}
		tokenHandler(w, r)
	})
	server = httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

func AdminAudience(origin string) string {
	audience, _ := protocol.PlaneOrigin(origin, protocol.AdminPlane, "")
	return audience
}

type ObservedOutput struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	OnWrite func(string)
}

func (w *ObservedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buffer.Write(p)
	text := w.buffer.String()
	w.mu.Unlock()
	w.OnWrite(text)
	return n, err
}

func (w *ObservedOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func MountDeviceAuth(t *testing.T, mux *http.ServeMux, issuer func() string, plane protocol.Plane,
	check func(*http.Request), expiresIn, interval int64, tokenHandler http.HandlerFunc,
) {
	t.Helper()
	mux.HandleFunc(protocol.APIPathInfo, func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		WriteJSON(
			w,
			map[string]any{"installation": protocol.InstallationReference{Origin: "https://" + r.Host, UID: "test-installation"}, "oauth": map[string]any{
				"issuer": issuer(), "adminAudience": AdminAudience(strings.TrimSuffix(issuer(), "/")), "cliClientID": "cli-test",
			}, "version": "test"},
		)
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(
			w,
			map[string]any{
				"issuer":                        issuer(),
				"token_endpoint":                strings.TrimSuffix(issuer(), "/") + "/token-exchange",
				"device_authorization_endpoint": strings.TrimSuffix(issuer(), "/") + "/device-authorization",
				"revocation_endpoint":           strings.TrimSuffix(issuer(), "/") + "/revoke",
			},
		)
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		if r.FormValue("client_id") != "cli-test" || r.FormValue("token_type_hint") != "refresh_token" || r.FormValue("token") == "" {
			t.Errorf("unexpected revocation form: %s", r.Form.Encode())
		}
	})
	mux.HandleFunc("/device-authorization", func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		origin, resource := strings.TrimSuffix(issuer(), "/"), r.FormValue("resource")
		target, _ := protocol.PlaneOrigin(origin, plane, "")
		_, _, session := protocol.ParseSessionAudience(origin, resource)
		if r.FormValue("client_id") != "cli-test" || resource != target && !session || r.FormValue("audience") != "" {
			t.Errorf("unexpected device request form: %s", r.Form.Encode())
		}
		WriteJSON(
			w,
			map[string]any{
				"device_code":      "device-secret",
				"user_code":        "ABCD-EFGH",
				"verification_uri": issuer() + "/activate",
				"expires_in":       expiresIn,
				"interval":         interval,
			},
		)
	})
	mux.HandleFunc("/token-exchange", func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		origin, resource := strings.TrimSuffix(issuer(), "/"), r.FormValue("resource")
		_, control := protocol.ParseControlAudience(origin, resource)
		_, _, session := protocol.ParseSessionAudience(origin, resource)
		if !control && !session || len(r.Form["resource"]) != 1 || r.FormValue("audience") != "" {
			t.Errorf("unexpected token request target: %s", r.Form.Encode())
		}
		tokenHandler(w, r)
	})
}

func WriteJSON(w http.ResponseWriter, value any) {
	w.Header().Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
	_ = json.NewEncoder(w).Encode(value)
}
