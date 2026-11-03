//go:build docker

package cloud

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/holon-sh/cli/pkg/protocol"
)

func TestDockerSourceBuildFailures(t *testing.T) {
	root := t.TempDir()
	manifest := buildManifest(t, root)
	_, grant, options := sourceRegistry(t)
	options.architectures = []string{runtime.GOARCH}
	upload := &imageUpload{manifest: manifest, grant: grant}

	t.Run("denied grant", func(t *testing.T) {
		calls := 0
		denied := &imageUpload{manifest: manifest, load: func(context.Context) (protocol.UploadGrant, error) {
			calls++
			current := grant
			current.Grant = "denied-grant"
			current.Repositories = map[string]string{"alpha": "apps/denied"}
			return current, nil
		}}
		apps, err := buildAndPush(t.Context(), manifest, denied, options)
		if err == nil || len(apps) != 0 || calls != 2 {
			t.Fatalf("denied grant: apps=%v, grants=%d: %v", apps, calls, err)
		}
	})
	t.Run("progress failure", func(t *testing.T) {
		want := errors.New("progress writer failed")
		failed := options
		failed.Stderr = progressFunc(func([]byte) (int, error) { return 0, want })
		if _, err := buildSource(t.Context(), manifest.Apps[0], upload, failed); !errors.Is(err, want) {
			t.Fatalf("progress error: %v", err)
		}
	})
	t.Run("build cancellation", func(t *testing.T) {
		sourceFiles(t, root, map[string]string{"package.json": `{
 "type":"module","engines":{"node":">=22.20.0 <23"},
 "scripts":{"build":"node -e \"console.log('cancel-build'); setInterval(() => {}, 1000)\""}}`})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		canceled := options
		canceled.architectures = []string{runtime.GOARCH}
		canceled.Stderr = progressFunc(func(data []byte) (int, error) {
			if strings.Contains(string(data), "cancel-build") {
				cancel()
			}
			return len(data), nil
		})
		if apps, err := buildAndPush(ctx, manifest, upload, canceled); !errors.Is(err, context.Canceled) || len(apps) != 0 {
			t.Fatalf("canceled build: %v: %v", apps, err)
		}
	})
}

func sourceRegistry(t *testing.T) (*imageRegistry, protocol.UploadGrant, buildOptions) {
	t.Helper()
	registry := newImageRegistry()
	registry.bearer = true
	registry.credentials["apps"] = registryCredential{protocol.UploadUsername, "grant-secret"}
	server := httptest.NewTLSServer(registry)
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	host := "localhost:" + port
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == host {
			address = server.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	t.Cleanup(transport.CloseIdleConnections)
	name := fmt.Sprintf("holon-source-contract-%d", time.Now().UnixNano())
	dockerTest(t, "buildx", "create", "--name", name, "--driver", "docker-container",
		"--driver-opt", "image=moby/buildkit:v0.33.1")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "buildx", "rm", "--force", name).Run()
	})
	builders := new(builderConnections)
	t.Cleanup(builders.close)
	return registry, protocol.UploadGrant{Grant: "grant-secret", Registry: host, Repositories: map[string]string{"alpha": "apps/alpha"}}, buildOptions{
		Env: append(os.Environ(), "BUILDX_BUILDER="+name), Stderr: os.Stderr, HTTPClient: &http.Client{Transport: transport}, builders: builders,
	}
}
