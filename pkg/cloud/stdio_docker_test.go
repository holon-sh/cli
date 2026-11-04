//go:build docker

package cloud

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"

	"github.com/holon-sh/cli/internal/commandenv"
	"github.com/holon-sh/protocol"
)

func TestDockerTransportContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "server")
	build := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-o", binary, "./testdata/mcp")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	published := map[string]any{
		"runtimeArguments": []any{
			map[string]any{"type": "named", "name": "-p", "value": "8085:8085"},
			map[string]any{"type": "named", "name": "-e", "value": "INJECTED_VALUE=published"},
		},
		"packageArguments":     []any{map[string]any{"type": "positional", "value": "published"}},
		"environmentVariables": []any{map[string]any{"name": "INJECTED_VALUE", "value": "published"}},
	}
	for _, test := range []struct {
		name, transport, command string
		pkg                      map[string]any
		app                      string
		injected, token          string
		arguments                []any
	}{
		{
			name: "stdio", transport: "stdio", command: "stdio",
			pkg: map[string]any{"packageArguments": []any{
				map[string]any{"type": "named", "name": "--project", "isRequired": true},
				map[string]any{"type": "named", "name": "--token", "isRequired": true, "isSecret": true},
				map[string]any{"type": "named", "name": "--optional", "isSecret": true},
				map[string]any{"type": "named", "name": "--empty"},
				map[string]any{"type": "positional", "value": "fixed"},
			}},
			app: `    arguments:
      --project: "two words; $(no-shell)"
      --token: {secret: TOKEN}
      --optional: {secret: OPTIONAL}
      --empty: ""
`,
			token:     "runtime-secret",
			arguments: []any{"--project=two words; $(no-shell)", "--token=runtime-secret", "--empty=", "fixed"},
		},
		{
			name: "run-http-replaced-inputs", transport: "http", command: "stdio", pkg: published,
			app: `    run:
      transport: {type: streamable-http, url: http://localhost:3000/mcp}
      args: [http]
      env:
        INJECTED_VALUE: literal-run
        TOKEN: {secret: TOKEN}
`,
			injected: "literal-run", token: "runtime-secret", arguments: []any{"http"},
		},
		{
			name: "run-stdio-replaced-no-env", transport: "stdio", command: "http", pkg: published,
			app: `    run:
      transport: {type: stdio}
      args: [stdio]
`,
			arguments: []any{"stdio"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry, server := buildRegistry(t)
			source := addTestImage(t, registry, runtime.GOARCH, ocispec.ImageConfig{
				Entrypoint: []string{"/server"}, Cmd: []string{test.command},
				Env:        []string{"FIXTURE_VALUE=preserved"},
				WorkingDir: "/work",
			}, map[string][]byte{"server": payload, "work/marker": []byte("preserved")})
			host := strings.TrimPrefix(server.URL, "https://")
			document := descriptorDocument("echo")
			pkg := document["packages"].([]any)[0].(map[string]any)
			pkg["identifier"] = host + "/source@" + source.Digest.String()
			pkg["transport"] = map[string]any{"type": "stdio"}
			maps.Copy(pkg, test.pkg)
			catalog := descriptorRegistry(t, document).RegistryRef
			manifest, err := parseManifest([]byte(fmt.Sprintf(`apps:
  - name: echo
    registry:
      name: %s
      registry: %s
      version: %s
      package: {registryType: oci}
%s    access: {server: {}}
`, catalog.Name, catalog.Registry, catalog.Version, test.app)), "holon.yaml")
			if err != nil {
				t.Fatal(err)
			}
			manifest, err = resolveManifestGraph(t.Context(), manifest)
			if err != nil {
				t.Fatal(err)
			}
			grant := protocol.UploadGrant{Registry: host, Grant: "grant-secret", Repositories: map[string]string{"echo": "holon/upload/echo"}}
			apps, err := buildAndPush(t.Context(), manifest, &imageUpload{manifest: manifest, grant: grant}, buildOptions{
				HTTPClient: server.Client(),
				origin:     "https://holon.example",
				Env:        []string{"DOCKER_CONFIG=" + t.TempDir()},
			})
			if err != nil {
				t.Fatal(err)
			}
			if test.transport == "http" && apps[0].Image != host+"/holon/upload/echo@"+source.Digest.String() {
				t.Fatal("native HTTP image changed")
			}
			repository, err := imageRepository(host+"/holon/upload/echo", grant.Grant, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			descriptor, err := repository.Resolve(t.Context(), apps[0].Image)
			if err != nil {
				t.Fatal(err)
			}
			image := "holon-bridge-contract:" + descriptor.Digest.Encoded()
			loadDockerImage(t, repository, descriptor, image)
			runArgs := []string{
				"run", "--platform", "linux/" + runtime.GOARCH, "--detach", "--read-only", "--user", "10001:10001", "--cap-drop", "ALL",
				"--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,nosuid,nodev,size=16m", "-p", "127.0.0.1::3000",
			}
			configured := map[string]string{}
			for name, input := range apps[0].Env {
				if input.Value != nil {
					configured[name] = *input.Value
				} else if name == "TOKEN" {
					configured[name] = "runtime-secret"
				}
			}
			for name, value := range configured {
				runArgs = append(runArgs, "--env", name+"="+value)
			}
			runArgs = append(runArgs, image)
			for _, arg := range apps[0].Args {
				if expanded, complete := protocol.Expand(arg, func(name string) (string, bool) {
					value, present := configured[name]
					return value, present
				}); complete {
					runArgs = append(runArgs, expanded)
				}
			}
			container := dockerTest(t, runArgs...)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if t.Failed() {
					logs, _ := exec.CommandContext(ctx, "docker", "logs", container).CombinedOutput()
					t.Log(string(logs))
				}
				_ = exec.CommandContext(ctx, "docker", "rm", "--force", container).Run()
			})
			address := dockerTest(t, "port", container, "3000/tcp")
			endpoint := "http://" + address + "/mcp"
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			var session *mcp.ClientSession
			for {
				client := mcp.NewClient(&mcp.Implementation{Name: "contract", Version: "1"}, nil)
				session, err = client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
				if err == nil {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("not ready: %v", err)
				case <-time.After(100 * time.Millisecond):
				}
			}
			defer session.Close()

			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"value": "hello"}})
			if err != nil || result.IsError {
				t.Fatalf("echo: %#v %v", result, err)
			}
			if diff := cmp.Diff(map[string]any{
				"value": "hello", "environment": "preserved", "directory": "/work", "uid": float64(10001),
				"injected": test.injected, "token": test.token, "empty": "", "arguments": test.arguments,
			}, result.StructuredContent); diff != "" {
				t.Fatalf("session configuration (-want +got):\n%s", diff)
			}
			if test.transport == "stdio" {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Origin", "https://untrusted.example")
				response, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusForbidden {
					t.Fatalf("cross-origin status = %d", response.StatusCode)
				}
			}
			logs, err := exec.CommandContext(ctx, "docker", "logs", container).CombinedOutput()
			if err != nil || !bytes.Contains(logs, []byte("fixture echo")) {
				t.Fatalf("stderr lost: %v\n%s", err, logs)
			}
			_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "crash", Arguments: map[string]any{}})
			if code := dockerTest(t, "wait", container); code == "0" {
				t.Fatal("process failure exited successfully")
			}
		})
	}
}

func loadDockerImage(t *testing.T, source content.ReadOnlyStorage, descriptor ocispec.Descriptor, tag string) {
	t.Helper()
	root := t.TempDir()
	layout, err := oci.NewWithContext(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := oras.CopyGraph(t.Context(), source, layout, descriptor, oras.DefaultCopyGraphOptions); err != nil {
		t.Fatal(err)
	}
	descriptor.Annotations = map[string]string{"io.containerd.image.name": "docker.io/library/" + tag}
	if err := layout.Tag(t.Context(), descriptor, tag); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "image.tar")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := tar.NewWriter(file)
	if err := archive.AddFS(os.DirFS(root)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	t.Log(dockerTest(t, "load", "--input", filename))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = commandenv.RunStreaming(ctx, "docker", []string{"image", "rm", tag}, nil, nil, io.Discard, io.Discard)
	})
}

func dockerTest(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	var output strings.Builder
	if err := commandenv.RunStreaming(ctx, "docker", args, nil, nil, &output, os.Stderr); err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, output.String())
	}
	return strings.TrimSpace(output.String())
}
