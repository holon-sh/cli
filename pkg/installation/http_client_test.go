package installation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func TestAPIClientRequestAndResponse(t *testing.T) {
	t.Parallel()
	const token = "access-secret"
	for _, tc := range []struct {
		name, body string
		status     int
		stream     bool
		mediaType  string
		want       error
		httpError  *httpError
	}{
		{name: "oversized response", status: http.StatusOK, body: strings.Repeat("x", maxHTTPBody+1), want: errResponseTooLarge},
		{
			name: "token redacted", status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"` + token + `"}}`,
			httpError: &httpError{StatusCode: http.StatusConflict, Code: "conflict"},
		},
		{
			name: "policy error", status: http.StatusBadRequest, body: `{"error":{"code":"invalid_request","message":"OAuth support is disabled"}}`,
			httpError: &httpError{StatusCode: http.StatusBadRequest, Code: "invalid_request", Message: "OAuth support is disabled"},
		},
		{
			name: "what the failed step printed", status: http.StatusBadRequest,
			body: `{"error":{"code":"invalid_request","message":"npm install: exit status 1"},"output":"npm error 404\n"}`,
			httpError: &httpError{
				StatusCode: http.StatusBadRequest, Code: "invalid_request", Message: "npm install: exit status 1", Output: "npm error 404\n",
			},
		},
		{
			name: "token redacted from what was printed", status: http.StatusBadRequest,
			body:      `{"error":{"code":"invalid_request","message":"npm install: exit status 1"},"output":"` + token + `"}`,
			httpError: &httpError{StatusCode: http.StatusBadRequest, Code: "invalid_request", Message: "npm install: exit status 1"},
		},
		{name: "redirect", status: http.StatusTemporaryRedirect, httpError: &httpError{StatusCode: http.StatusTemporaryRedirect}},
		{name: "unexpected streamed JSON", status: http.StatusOK, stream: true, mediaType: protocol.MIMEApplicationJSON, body: `{}`, want: errMalformedResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := installationtest.HTTPClient(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.String() != "https://dev.apps.example.test/request" {
					t.Errorf("request = %s %s", r.Method, r.URL)
					return nil, errors.New("unexpected request")
				}
				if r.Header.Get(protocol.HeaderAuthorization) != protocol.BearerPrefix+token {
					t.Errorf("headers: %#v", r.Header)
				}
				response := httptest.NewRecorder()
				response.Header().Set(protocol.HeaderContentType, tc.mediaType)
				response.Header().Set("Location", "https://other.example/request")
				response.WriteHeader(tc.status)
				_, _ = io.WriteString(response, tc.body)
				return response.Result(), nil
			})
			client := newAPIClient("https://apps.example.test", transport, 0)
			var body struct {
				Content string `json:"content"`
			}
			var output bytes.Buffer
			request := apiRequest{Plane: protocol.DevPlane, Token: token}
			if tc.stream {
				request.Stream = &output
			}
			err := client.send(t.Context(), "/request", request, &body)
			if tc.httpError != nil {
				got, ok := errors.AsType[*httpError](err)
				if !ok || *got != *tc.httpError || strings.Contains(err.Error()+got.Output, token) || !strings.Contains(err.Error(), tc.httpError.Message) {
					t.Fatalf("HTTP error = %v, want %#v", err, tc.httpError)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if body.Content+output.String() != "" {
				t.Fatalf("content length = %d", len(body.Content))
			}
		})
	}
}

func TestAPIClientTimeouts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		stream   bool
		duration time.Duration
		want     error
	}{
		{name: "request", duration: time.Second, want: context.DeadlineExceeded},
		{name: "stream", stream: true, duration: 2 * defaultHTTPTimeout, want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				transport := installationtest.HTTPClient(func(r *http.Request) (*http.Response, error) {
					if tc.stream {
						reader, writer := io.Pipe()
						context.AfterFunc(r.Context(), func() { _ = writer.CloseWithError(r.Context().Err()) })
						return &http.Response{
							StatusCode: http.StatusOK, Body: reader,
							Header: http.Header{protocol.HeaderContentType: []string{protocol.MIMETextPlain}},
						}, nil
					}
					<-r.Context().Done()
					return nil, r.Context().Err()
				})
				transport.Timeout = time.Second
				client := newAPIClient("https://api.example", transport, time.Second)
				request := apiRequest{Plane: protocol.DevPlane}
				if tc.stream {
					request.Stream = io.Discard
					defer time.AfterFunc(tc.duration, cancel).Stop()
				}
				start := time.Now()
				err := client.send(ctx, "/me", request, nil)
				if elapsed := time.Since(start); elapsed != tc.duration || !errors.Is(err, tc.want) {
					t.Fatalf("request took %v with error %v, want %v and %v", elapsed, err, tc.duration, tc.want)
				}
			})
		})
	}
}
