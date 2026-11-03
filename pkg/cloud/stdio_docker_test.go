//go:build docker

package cloud

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"

	"github.com/holon-sh/cli/pkg/commandenv"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/protocol"
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
	for _, test := range []struct {
		name, transport string
		manifestRun     bool
		noEnv           bool
	}{
		{name: "stdio", transport: "stdio"},
		{name: "run-http-replaced-inputs", transport: "http", manifestRun: true},
		{name: "run-stdio-replaced-no-env", transport: "stdio", manifestRun: true, noEnv: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := newImageRegistry()
			command := test.transport
			environment := []string{"FIXTURE_VALUE=preserved", "OPTIONAL=image-fallback"}
			if test.manifestRun {
				command = map[string]string{"http": "stdio", "stdio": "http"}[test.transport]
				environment = []string{"FIXTURE_VALUE=preserved"}
			}
			source := addTestImage(t, registry, runtime.GOARCH, ocispec.ImageConfig{
				Entrypoint: []string{"/server"}, Cmd: []string{command},
				Env:        environment,
				WorkingDir: "/work",
			}, map[string][]byte{"server": payload, "work/marker": []byte("preserved")})
			server := httptest.NewTLSServer(registry)
			defer server.Close()
			host := strings.TrimPrefix(server.URL, "https://")
			document := descriptorDocument("echo")
			pkg := document["packages"].([]any)[0].(map[string]any)
			pkg["identifier"] = host + "/source@" + source.Digest.String()
			pkg["transport"] = map[string]any{"type": "stdio"}
			var manifest manifestSpec
			var err error
			if test.manifestRun {
				document["name"] = "example.org/echo"
				pkg["runtimeArguments"] = []any{
					map[string]any{"type": "named", "name": "-p", "value": "8085:8085"},
					map[string]any{"type": "named", "name": "-e", "value": "INJECTED_VALUE=published"},
				}
				pkg["packageArguments"] = []any{map[string]any{"type": "positional", "value": "published"}}
				pkg["environmentVariables"] = []any{map[string]any{"name": "INJECTED_VALUE", "value": "published"}}
				catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_ = json.NewEncoder(w).Encode(map[string]any{"server": document})
				}))
				t.Cleanup(catalog.Close)
				transport := "{type: stdio}"
				if test.transport == "http" {
					transport = "{type: streamable-http, url: http://localhost:3000/mcp}"
				}
				env := ""
				if !test.noEnv {
					env = "      env:\n        INJECTED_VALUE: literal-run\n        TOKEN: {secret: TOKEN}\n"
				}
				manifest, err = parseManifest([]byte(fmt.Sprintf(`apps:
  - name: echo
    registry:
      name: example.org/echo
      registry: %s
      version: "1.4.2"
      package: {registryType: oci}
    run:
      transport: %s
      args: [%s]
%s    access: {server: {}}
`, catalog.URL, transport, test.transport, env)), "holon.yaml")
			} else {
				pkg["packageArguments"] = []any{
					map[string]any{"type": "named", "name": "--project", "isRequired": true},
					map[string]any{"type": "named", "name": "--token", "isRequired": true, "isSecret": true},
					map[string]any{"type": "named", "name": "--optional", "isSecret": true},
					map[string]any{"type": "named", "name": "--empty"},
					map[string]any{"type": "positional", "value": "fixed"},
				}
				manifest = manifestSpec{Apps: []manifestApp{{Name: "echo", Registry: descriptorRegistry(t, document), Arguments: map[string]mcpregistry.Value{
					"--project": {Value: new("two words; $(no-shell)")}, "--empty": {Value: new("")},
					"--token": {Secret: "TOKEN"}, "--optional": {Secret: "OPTIONAL"},
				}}}}
			}
			if err == nil {
				manifest, err = resolveManifestGraph(t.Context(), manifest)
			}
			if err != nil {
				t.Fatal(err)
			}
			grant := protocol.UploadGrant{Registry: host, Grant: "grant-secret", Repositories: map[string]string{"echo": "holon/upload/echo"}}
			apps, err := buildAndPush(t.Context(), manifest, &imageUpload{manifest: manifest, grant: grant}, buildOptions{
				HTTPClient: server.Client(),
				origin:     "https://holon.example",
				Env: []string{
					"DOCKER_CONFIG=" + t.TempDir(),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			imageManifest, _ := preparedConfig(t, registry, apps[0].Image)
			if test.transport == "http" && apps[0].Image != host+"/holon/upload/echo@"+source.Digest.String() {
				t.Fatal("native HTTP image changed")
			}
			image := "holon-bridge-contract:" + imageManifest.Config.Digest.Encoded()
			repository, err := imageRepository(host+"/holon/upload/echo", grant.Grant, nil, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, ref, _ := strings.Cut(apps[0].Image, "@")
			descriptor, err := repository.Resolve(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			loadDockerImage(t, repository, descriptor, image)
			runArgs := []string{
				"run", "--platform", "linux/" + runtime.GOARCH, "--detach", "--read-only", "--user", "10001:10001", "--cap-drop", "ALL",
				"--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,nosuid,nodev,size=16m", "-p", "127.0.0.1::3000",
			}
			// Configure the container as the controller does: the app's literals and secrets reach its environment, and
			// the kubelet expands each argument from that environment, dropping one that names an unconfigured input.
			configured := map[string]string{}
			for name, input := range apps[0].Env {
				if input.Value != nil {
					configured[name] = *input.Value
				} else if input.IsSecret && test.manifestRun {
					configured[name] = "runtime-secret"
				}
			}
			if !test.manifestRun {
				configured["INJECTED_VALUE"], configured["TOKEN"], configured["EMPTY"] = "two words; $(no-shell)", "runtime-secret", ""
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
			output, _ := json.Marshal(result.StructuredContent)
			var state map[string]any
			if err := json.Unmarshal(output, &state); err != nil {
				t.Fatal(err)
			}
			if state["value"] != "hello" || state["environment"] != "preserved" || state["directory"] != "/work" || state["uid"] != float64(10001) {
				t.Fatalf("session configuration = %s", output)
			}
			argv, _ := json.Marshal(state["arguments"])
			if test.manifestRun {
				wantInjected, wantToken := "literal-run", "runtime-secret"
				if test.noEnv {
					wantInjected, wantToken = "", ""
				}
				if state["injected"] != wantInjected || state["token"] != wantToken || string(argv) != `["`+test.transport+`"]` {
					t.Fatalf("manifest launch configuration: %s", output)
				}
			} else {
				if state["injected"] != "two words; $(no-shell)" || state["token"] != "runtime-secret" || state["empty"] != "" ||
					string(argv) != `["--project=two words; $(no-shell)","--token=runtime-secret","--empty=","fixed"]` {
					t.Fatalf("injected inputs: %s", output)
				}
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
				logs, err := exec.CommandContext(ctx, "docker", "logs", container).CombinedOutput()
				if err != nil || !bytes.Contains(logs, []byte("fixture echo")) {
					t.Fatalf("stderr lost: %v\n%s", err, logs)
				}
				_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "crash", Arguments: map[string]any{}})
				if code := dockerTest(t, "wait", container); code == "0" {
					t.Fatal("process failure exited successfully")
				}
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
