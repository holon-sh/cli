package installation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
)

// traceback is what a Python app writes to stderr when a tool raises.
const traceback = "Tool 'boom' raised an unexpected exception\nTraceback (most recent call last):\nRuntimeError: boom"

func TestLogs(t *testing.T) {
	h := newRunHarness(t)
	var requests []string
	h.control.Handle([]string{toolGetLogs}, nil, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, strings.TrimSuffix(r.Host, h.baseHost)+" "+string(body))
		installationtest.WriteJSON(w, map[string]any{"session": "production", "records": []map[string]string{
			{"time": "2026-11-09T10:00:00.1Z", "app": "alpha", "body": "starting boom"},
			{"time": "2026-11-09T10:00:00.2Z", "severity": "ERROR", "app": "alpha", "body": traceback, "traceId": "aaaa"},
		}})
	})
	ctx, cancel := context.WithCancel(t.Context())
	h.mux.HandleFunc("GET /sessions/gentle-otter-482/logs", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, strings.TrimSuffix(r.Host, h.baseHost)+" "+r.URL.RawQuery)
		_, _ = io.WriteString(w, "2026-11-09T10:00:00.1Z  alpha streamed\n")
	})
	h.start(t, nil)
	var out bytes.Buffer
	if err := executeCommand(t.Context(), []string{"logs", "production", "--app", "alpha", "--since", "1h"}, &out, nil, h.run); err != nil {
		t.Fatal(err)
	}
	if want := "2026-11-09T10:00:00.1Z  alpha starting boom\n2026-11-09T10:00:00.2Z ERROR alpha " + traceback + " trace_id=aaaa\n"; out.String() != want {
		t.Fatalf("snapshot = %q, want %q", out.String(), want)
	}
	followed := &installationtest.ObservedOutput{OnWrite: func(string) { cancel() }}
	if err := executeCommand(ctx, []string{"logs", "gentle-otter-482", "-f", "--attempt", "2"}, followed, nil, h.run); !errors.Is(err, context.Canceled) {
		t.Fatalf("follow = %v", err)
	}
	if got := followed.String(); got != "2026-11-09T10:00:00.1Z  alpha streamed\n" {
		t.Fatalf("followed = %q", got)
	}
	if want := []string{`admin. {"session":"production","app":"alpha","since":"1h"}`, "dev. attempt=2&follow=true"}; !slices.Equal(requests, want) {
		t.Fatalf("requests = %q, want %q", requests, want)
	}
}
