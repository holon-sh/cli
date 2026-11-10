package installation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

const traceback = "Tool 'boom' raised an unexpected exception\nTraceback (most recent call last):\nRuntimeError: boom"

// printedLog is how holon prints the log lines of the stream format: each record's time is the local time of day.
func printedLog(lines string) string {
	return regexp.MustCompile(`(?m)^\S+Z `).ReplaceAllStringFunc(lines, func(stamp string) string {
		at, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(stamp))
		return logTime(at) + " "
	})
}

func TestLogs(t *testing.T) {
	h := newRunHarness(t)
	h.control.Handle([]string{toolGetLogs}, map[string]string{"session": "production"}, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, map[string]any{"records": []map[string]string{
			{"time": "2026-11-09T10:00:00.2Z", "app": "alpha", "body": traceback, "traceId": "aaaa"},
		}})
	})
	h.control.Handle([]string{toolListSessions}, nil, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, map[string]any{"items": []protocol.SessionResponse{
			{Name: "gentle-otter-482", Apps: []protocol.App{{Name: "alpha"}}}, {Name: "quiet-heron-7"},
		}})
	})
	for _, session := range []string{"gentle-otter-482", "quiet-heron-7"} {
		h.mux.HandleFunc("GET /sessions/"+session+"/logs", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "2026-11-09T10:00:00.1Z  alpha "+session+"\n")
		})
	}
	h.start(t, nil)
	var out bytes.Buffer
	if err := executeCommand(t.Context(), []string{"logs", "alpha", "--env", "prod"}, &out, nil, h.run); err != nil {
		t.Fatal(err)
	}
	if want := printedLog("2026-11-09T10:00:00.2Z  alpha " + traceback + " trace_id=aaaa\n"); out.String() != want {
		t.Fatalf("production = %q, want %q", out.String(), want)
	}
	ctx, cancel := context.WithCancel(t.Context())
	followed := &installationtest.ObservedOutput{OnWrite: func(string) { cancel() }}
	if err := executeCommand(ctx, []string{"logs", "alpha", "-f"}, followed, nil, h.run); !errors.Is(err, context.Canceled) {
		t.Fatalf("follow = %v", err)
	}
	if want := printedLog("2026-11-09T10:00:00.1Z  alpha gentle-otter-482\n"); followed.String() != want {
		t.Fatalf("followed = %q, want %q", followed.String(), want)
	}
	if err := executeCommand(t.Context(), []string{"logs"}, nil, nil, h.run); err == nil ||
		err.Error() != "choose a development session with --session: gentle-otter-482, quiet-heron-7" {
		t.Fatalf("ambiguous session = %v", err)
	}
}
