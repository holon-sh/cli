package installation

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	bridgeproxy "github.com/holon-sh/mcp-bridge/proxy"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func TestControlCallStructuredContent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, structured string
		want             error
	}{
		{name: "object", structured: `,"structuredContent":{"name":"orders"}`},
		{name: "null", structured: `,"structuredContent":null`, want: errMalformedResponse},
		{name: "missing", want: errMalformedResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request.Method == "server/discover" {
					installationtest.WriteJSON(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": mcp.DiscoverResult{
						SupportedVersions: []string{bridgeproxy.CurrentVersion}, Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
					}})
					return
				}
				w.Header().Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(request.ID) + `,"result":{"resultType":"complete","content":[]` + tc.structured + `}}`))
			})
			transport := installationtest.HTTPClient(func(r *http.Request) (*http.Response, error) {
				response := httptest.NewRecorder()
				handler(response, r)
				return response.Result(), nil
			})
			var output struct {
				Name string `json:"name"`
			}
			err := newAPIClient("https://apps.example.test", transport, 0).Call(t.Context(), protocol.BasePlane, "token", protocol.ToolGetSession, protocol.GetSessionRequest{}, &output)
			if !errors.Is(err, tc.want) || (tc.want == nil && output.Name != "orders") {
				t.Fatalf("Call = %v, output %+v; want %v", err, output, tc.want)
			}
		})
	}
}
