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
		content    string
		want       error
		httpError  *httpError
	}{
		{name: "malformed JSON", status: http.StatusOK, body: `{bad`, want: errMalformedResponse},
		{name: "oversized response", status: http.StatusOK, body: strings.Repeat("x", maxHTTPBody+1), want: errResponseTooLarge},
		{
			name: "token redacted", status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"` + token + `"}}`,
			httpError: &httpError{StatusCode: http.StatusConflict, Code: "conflict"},
		},
		{
			name: "policy error", status: http.StatusBadRequest, body: `{"error":{"code":"invalid_request","message":"OAuth support is disabled"}}`,
			httpError: &httpError{StatusCode: http.StatusBadRequest, Code: "invalid_request", Message: "OAuth support is disabled"},
		},
		{name: "redirect", status: http.StatusTemporaryRedirect, httpError: &httpError{StatusCode: http.StatusTemporaryRedirect}},
		{name: "unexpected streamed JSON", status: http.StatusOK, stream: true, mediaType: protocol.MIMEApplicationJSON, body: `{}`, want: errMalformedResponse},
		{
			name: "stream error", status: http.StatusNotFound, stream: true, mediaType: protocol.MIMEApplicationJSON,
			body:      `{"error":{"code":"not_found","message":"not found"}}`,
			httpError: &httpError{StatusCode: http.StatusNotFound, Code: "not_found", Message: "not found"},
		},
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
			client, err := newAPIClient("https://apps.example.test", transport, 0)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Content string `json:"content"`
			}
			var output bytes.Buffer
			request := apiRequest{Plane: protocol.DevPlane, Token: token}
			if tc.stream {
				request.Stream = &output
			}
			err = client.Do(t.Context(), http.MethodGet, "/request", request, &body)
			if tc.httpError != nil {
				got, ok := errors.AsType[*httpError](err)
				if !ok || *got != *tc.httpError || strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), tc.httpError.Message) {
					t.Fatalf("HTTP error = %v, want %#v", err, tc.httpError)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if body.Content+output.String() != tc.content {
				t.Fatalf("content length = %d", len(body.Content))
			}
		})
	}
}

func TestAPIClientCancellation(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"during request", "deadline", "stream"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				entered := make(chan struct{})
				transport := installationtest.HTTPClient(func(r *http.Request) (*http.Response, error) {
					close(entered)
					if stage == "stream" {
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
				client, err := newAPIClient("https://api.example", transport, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				request := apiRequest{Plane: protocol.DevPlane}
				switch stage {
				case "during request":
					go func() {
						<-entered
						cancel()
					}()
				case "stream":
					request.Stream = io.Discard
					timer := time.AfterFunc(2*defaultHTTPTimeout, cancel)
					defer timer.Stop()
				}
				start := time.Now()
				err = client.Do(ctx, http.MethodGet, "/me", request, nil)
				wantDuration := map[string]time.Duration{"deadline": time.Second, "stream": 2 * defaultHTTPTimeout}[stage]
				if elapsed := time.Since(start); elapsed != wantDuration {
					t.Fatalf("request duration = %v, want %v", elapsed, wantDuration)
				}
				want := context.Canceled
				if stage == "deadline" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatalf("request error = %v, want %v", err, want)
				}
			})
		})
	}
}

func TestAPIClientCanonicalURL(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		" https://example.com", "http://example.com", "https://user@example.com", "https://example.com//",
		"https://example.com?x=1", "https://example.com#x", "https://192.0.2.1",
		"https://localhost:99999", "https://cluster-local", "https://EXAMPLE.com",
	} {
		if _, err := newAPIClient(value, nil, 0); err == nil {
			t.Fatalf("URL %q error %v", value, err)
		}
	}
	for _, value := range []string{"https://example.com:443", "https://localhost:30443", "https://127.0.0.1:8080"} {
		if client, err := newAPIClient(value, nil, 0); err != nil || client.baseURL != value {
			t.Fatalf("URL %q client=%#v error=%v", value, client, err)
		}
	}
}
