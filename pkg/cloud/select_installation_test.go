package cloud

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type installationTransport struct {
	version  string
	attempts int
}

func (transport *installationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.attempts++
	status := http.StatusOK
	if transport.attempts == 1 || transport.version == "unreachable" {
		status = http.StatusServiceUnavailable
	}
	payload := `{"version":"` + transport.version + `","buildArchitectures":["arm64"],` +
		`"oauth":null,"installation":{"origin":"https://holon.localhost","uid":"installation"}}`
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
}

func TestStartSelectsReadyInstallation(t *testing.T) {
	for _, tc := range []struct {
		name, version, wantError string
		writeFailure             bool
	}{
		{name: "ready", version: "dev"},
		{name: "version mismatch", version: "different", wantError: "expected dev"},
		{name: "unreachable", version: "unreachable", wantError: "not reachable"},
		{name: "write failure", version: "dev", writeFailure: true, wantError: "save Holon configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				if tc.writeFailure {
					if err := os.Mkdir(filepath.Join(dir, "config.json"), 0o700); err != nil {
						t.Fatal(err)
					}
				} else {
					sourceFiles(t, dir, map[string]string{"config.json": `{"origin":"https://previous.example"}`})
				}
				var output bytes.Buffer
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				err := SelectInstallation(ctx, "https://holon.localhost", "dev", &output, dir, &http.Client{Transport: &installationTransport{version: tc.version}})
				if tc.wantError == "" {
					if err != nil || !strings.Contains(output.String(), "Selected Holon installation https://holon.localhost") {
						t.Fatalf("selection: %s, %v", output.String(), err)
					}
					assertSavedOrigin(t, dir, "https://holon.localhost")
				} else {
					if err == nil || !strings.Contains(err.Error(), tc.wantError) || strings.Contains(output.String(), "Selected Holon installation") {
						t.Fatalf("failed selection: %s, %v", output.String(), err)
					}
					if !tc.writeFailure {
						assertSavedOrigin(t, dir, "https://previous.example")
					}
				}
			})
		})
	}
}
