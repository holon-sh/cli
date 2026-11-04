package cloud

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	bridgeproxy "github.com/holon-sh/mcp-bridge/proxy"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/protocol"
)

func (c *apiClient) Call(ctx context.Context, plane protocol.Plane, token, tool string, input, output any) error {
	origin, _ := protocol.PlaneOrigin(c.baseURL, plane, "")
	data, err := json.Marshal(input)
	if err != nil || len(data) > maxRequestBody {
		return errMalformedResponse
	}
	timeout := c.timeout
	switch tool {
	case protocol.ToolCreateSession, protocol.ToolUpdateSession, protocol.ToolDeleteSession:
		timeout = max(timeout, 5*time.Minute)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := *c.client
	client.Transport = &controlTransport{base: cmp.Or(client.Transport, http.DefaultTransport), token: token, installation: c.installationUID}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "holon", Version: "cli"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: origin + protocol.MCPPath, HTTPClient: &client},
		&mcp.ClientSessionOptions{ProtocolVersion: bridgeproxy.CurrentVersion})
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: json.RawMessage(data)})
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil || result.StructuredContent == nil {
		return errMalformedResponse
	}
	if result.IsError {
		code, message := responseError(encoded, token, http.StatusBadRequest)
		if code == "" {
			return errors.New("control tool rejected the request")
		}
		return &protocol.Error{Code: code, Message: message}
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(encoded, output); err != nil {
		return fmt.Errorf("%w: %w", errMalformedResponse, err)
	}
	return nil
}

type controlTransport struct {
	base         http.RoundTripper
	token        string
	installation string
}

func (t *controlTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	if t.token != "" {
		request.Header.Set(protocol.HeaderAuthorization, protocol.BearerPrefix+t.token)
	}
	request.Header.Set(protocol.HeaderInstallationUID, t.installation)
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := clientutil.ReadLimited(response.Body, 2*maxHTTPBody)
	if errors.Is(err, clientutil.ErrContentTooLarge) {
		return nil, errResponseTooLarge
	}
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code, message := responseError(data, t.token, response.StatusCode)
		return nil, &httpError{StatusCode: response.StatusCode, Code: code, Message: message}
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}
