package cloud

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"

	"github.com/holon-sh/cli/pkg/ociimage"
	"github.com/holon-sh/cli/pkg/protocol"
)

func preparedConfig(t *testing.T, registry *imageRegistry, image string) (ocispec.Manifest, ocispec.Image) {
	t.Helper()
	repository, reference, ok := strings.Cut(image, "@")
	if !ok {
		t.Fatalf("image has no digest: %q", image)
	}
	_, repository, ok = strings.Cut(repository, "/")
	if !ok {
		t.Fatalf("image has no repository: %q", image)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	var manifest ocispec.Manifest
	if err := json.Unmarshal(registry.items[repository+"/manifests/"+reference].data, &manifest); err != nil {
		t.Fatal(err)
	}
	var config ocispec.Image
	if err := json.Unmarshal(registry.items[repository+"/blobs/"+manifest.Config.Digest.String()].data, &config); err != nil {
		t.Fatal(err)
	}
	return manifest, config
}

func TestStdioImageContract(t *testing.T) {
	type imageCase struct {
		name, architecture        string
		plain                     bool
		entrypoint, cmd, expected []string
		files                     map[string][]byte
		wantErr                   string
	}
	stdio := []string{ociimage.RuntimePath, "--origin", "https://holon.example", "--"}
	// The image's command stays the default arguments, which an app's args replace.
	stdioCommand := imageCase{
		name: "entrypoint and cmd", architecture: "amd64", entrypoint: []string{"/server", "--flag"}, cmd: []string{"default"},
		expected: slices.Concat(stdio, []string{"/server", "--flag"}),
	}
	commands := []imageCase{
		{name: "cmd only", architecture: "arm64", cmd: []string{"/server", "arg"}, expected: stdio},
		{name: "missing executable", architecture: "amd64", wantErr: "no executable"},
		{
			name: "reserved file", architecture: "amd64", cmd: []string{"/server"},
			files: map[string][]byte{"holon-mcp-runtime": []byte("collision")}, wantErr: "reserved path",
		},
		{
			name: "reserved directory", architecture: "amd64", cmd: []string{"/server"},
			files: map[string][]byte{"holon-mcp-runtime/file": []byte("collision")}, wantErr: "reserved path",
		},
		{
			name: "reserved whiteout", architecture: "amd64", cmd: []string{"/server"},
			files: map[string][]byte{".wh.holon-mcp-runtime": []byte("collision")}, wantErr: "reserved path",
		},
	}
	type imageTest struct {
		name   string
		inputs []imageCase
	}
	tests := make([]imageTest, 0, len(commands)+1)
	for _, command := range commands {
		tests = append(tests, imageTest{command.name, []imageCase{command}})
	}
	tests = append(tests, imageTest{"mixed stdio and plain images", []imageCase{stdioCommand, {
		name: "plain", architecture: "arm64", plain: true, entrypoint: []string{"/server"},
	}}})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inputs := test.inputs
			registry, server := buildRegistry(t)
			host := strings.TrimPrefix(server.URL, "https://")
			grant := protocol.UploadGrant{Registry: host, Grant: "grant-secret", Repositories: map[string]string{}}
			manifest := manifestSpec{}
			for index, command := range inputs {
				original := ocispec.ImageConfig{
					Entrypoint: command.entrypoint, Cmd: command.cmd,
					Env: []string{"PATH=/bin"}, WorkingDir: "/work", User: "10001:10001",
					Labels: map[string]string{"custom": "label"}, ExposedPorts: map[string]struct{}{"9000/tcp": {}}, StopSignal: "SIGQUIT",
				}
				files := map[string][]byte{"original": []byte("unchanged")}
				maps.Copy(files, command.files)
				source := addTestImage(t, registry, command.architecture, original, files)
				name := fmt.Sprintf("app-%d", index)
				grant.Repositories[name] = "holon/upload/" + name
				app := manifestApp{
					Name: name, Image: host + "/source@" + source.Digest.String(),
					resolved: resolvedApp{stdio: !command.plain, App: protocol.App{
						Name: name, Image: host + "/source@" + source.Digest.String(),
					}},
				}
				if !command.plain {
					app.resolved.Port = 3000
				}
				manifest.Apps = append(manifest.Apps, app)
			}
			options := buildOptions{
				HTTPClient: server.Client(),

				origin: "https://holon.example",
				Env: []string{
					"DOCKER_CONFIG=" + t.TempDir(),
				},
			}
			apps, err := buildAndPush(t.Context(), manifest, &imageUpload{manifest: manifest, grant: grant}, options)
			if inputs[0].wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), inputs[0].wantErr) {
					t.Fatalf("want %s: %v", inputs[0].wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(apps) != len(inputs) {
				t.Fatalf("app count = %d, want %d", len(apps), len(inputs))
			}
			for index, command := range inputs {
				originalManifest, originalConfig := preparedConfig(t, registry, manifest.Apps[index].Image)
				_, sourceDigest, _ := strings.Cut(manifest.Apps[index].Image, "@")
				imageManifest, config := preparedConfig(t, registry, apps[index].Image)
				want := originalConfig.Config
				if !command.plain {
					want.Entrypoint, want.StopSignal = command.expected, "SIGTERM"
					want.Labels["sh.holon.bridge.source"] = host + "/" + grant.Repositories[apps[index].Name] + "@" + sourceDigest
				}
				delete(config.Config.Labels, "sh.holon.bridge.runtime")
				if diff := cmp.Diff(want, config.Config); diff != "" {
					t.Fatalf("image configuration (-want +got):\n%s", diff)
				}
				layers := 2
				if command.plain {
					layers = 1
				}
				if len(imageManifest.Layers) != layers || !cmp.Equal(imageManifest.Layers[:1], originalManifest.Layers) ||
					imageManifest.Annotations["example"] != "preserved" || len(config.RootFS.DiffIDs) != layers || len(config.History) != layers {
					t.Fatalf("layers or metadata lost: %#v %#v", imageManifest, config)
				}
			}
		})
	}
	t.Run("layer integrity", func(t *testing.T) {
		raw := make([]byte, 1024)
		var compressed bytes.Buffer
		zipper := gzip.NewWriter(&compressed)
		if _, err := zipper.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := zipper.Close(); err != nil {
			t.Fatal(err)
		}
		zipped, err := zstd.NewWriter(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer zipped.Close()
		registry, server := buildRegistry(t)
		source := addTestImage(t, registry, "amd64", ocispec.ImageConfig{Cmd: []string{"/server"}}, nil)
		repository := strings.TrimPrefix(server.URL, "https://") + "/source"
		manifest, _ := preparedConfig(t, registry, repository+"@"+source.Digest.String())
		options := buildOptions{HTTPClient: server.Client(), checkedLayers: new(sync.Map)}
		prepare := func(ctx context.Context, descriptor ocispec.Descriptor, data []byte) error {
			t.Helper()
			manifest.Layers = []ocispec.Descriptor{descriptor}
			encoded, _ := json.Marshal(manifest)
			image := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, encoded)
			registry.mu.Lock()
			registry.items["source/manifests/"+image.Digest.String()] = registryItem{mediaType: image.MediaType, data: encoded}
			registry.items["source/blobs/"+descriptor.Digest.String()] = registryItem{mediaType: descriptor.MediaType, data: data}
			registry.mu.Unlock()
			_, err := prepareStdioImage(ctx, repository+"@"+image.Digest.String(), "", options)
			return err
		}
		for _, layer := range []struct {
			mediaType string
			data      []byte
		}{
			{ocispec.MediaTypeImageLayer, raw},
			{ocispec.MediaTypeImageLayerGzip, compressed.Bytes()},
			{ocispec.MediaTypeImageLayerZstd, zipped.EncodeAll(raw, nil)},
		} {
			t.Run(layer.mediaType, func(t *testing.T) {
				descriptor := content.NewDescriptorFromBytes(layer.mediaType, layer.data)
				corrupt := slices.Clone(layer.data)
				corrupt[len(corrupt)-1] ^= 1
				for range 2 {
					if err := prepare(t.Context(), descriptor, corrupt); err == nil {
						t.Fatal("invalid layer accepted")
					}
				}
				if err := prepare(t.Context(), descriptor, layer.data); err != nil {
					t.Fatalf("valid retry: %v", err)
				}
			})
		}
		descriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, raw)
		for _, changed := range []ocispec.Descriptor{
			{MediaType: descriptor.MediaType, Digest: digest.FromString("changed"), Size: descriptor.Size},
			{MediaType: descriptor.MediaType, Digest: descriptor.Digest, Size: descriptor.Size + 1},
			{MediaType: "unsupported", Digest: descriptor.Digest, Size: descriptor.Size},
		} {
			if err := prepare(t.Context(), changed, raw); err == nil {
				t.Fatal("changed descriptor reused a successful scan")
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := prepare(ctx, descriptor, raw); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled preparation: %v", err)
		}
		if err := prepare(t.Context(), descriptor, raw); err != nil {
			t.Fatalf("retry after cancellation: %v", err)
		}
	})
}

func TestStdioMultiArchitectureBuild(t *testing.T) {
	registry := newImageRegistry()
	var children []ocispec.Descriptor
	for _, architecture := range []string{"amd64", "arm64"} {
		children = append(children, addTestImage(t, registry, architecture, ocispec.ImageConfig{Cmd: []string{"/server"}}, nil))
	}
	attestation := addTestImage(t, registry, "unknown", ocispec.ImageConfig{}, nil)
	attestation.Platform = &ocispec.Platform{OS: "unknown", Architecture: "unknown"}
	attestation.Annotations = map[string]string{"vnd.docker.reference.type": "attestation-manifest"}
	children = append(children, attestation)
	descriptor := addTestIndex(registry, children...)
	server := httptest.NewTLSServer(registry)
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "https://")
	grant := protocol.UploadGrant{Registry: host, Grant: "grant-secret", Repositories: map[string]string{"echo": "holon/upload/echo"}}
	options := buildOptions{HTTPClient: server.Client(), Env: []string{"DOCKER_CONFIG=" + t.TempDir(), "DOCKER_CONTEXT=fixture"}}
	source := host + "/source@" + descriptor.Digest.String()
	app := manifestApp{Name: "echo", Image: source, resolved: resolvedApp{stdio: true, App: protocol.App{Name: "echo", Image: source, Port: 3000}}}
	prepared, err := buildAndPush(t.Context(), manifestSpec{Apps: []manifestApp{app}}, &imageUpload{grant: grant}, options)
	if err != nil {
		t.Fatal(err)
	}
	var result ocispec.Index
	_, reference, ok := strings.Cut(prepared[0].Image, "@")
	if !ok {
		t.Fatalf("image has no digest: %q", prepared[0].Image)
	}
	if err := json.Unmarshal(registry.items["holon/upload/echo/manifests/"+reference].data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Manifests) != 2 {
		t.Fatal("multi-architecture index lost")
	}
	for i, child := range result.Manifests {
		if !cmp.Equal(child.Platform, children[i].Platform) || child.Digest == children[i].Digest {
			t.Fatalf("child not adapted: %#v", child)
		}
		manifest, config := preparedConfig(t, registry, host+"/holon/upload/echo@"+child.Digest.String())
		if config.Config.Entrypoint[0] != ociimage.RuntimePath {
			t.Fatal("imported image was not adapted")
		}
		zipper, err := gzip.NewReader(bytes.NewReader(registry.items["holon/upload/echo/blobs/"+manifest.Layers[1].Digest.String()].data))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(zipper)
		zipper.Close()
		if err != nil || digest.FromBytes(raw) != config.RootFS.DiffIDs[1] {
			t.Fatalf("runtime diff ID: %v", err)
		}
		archive := tar.NewReader(bytes.NewReader(raw))
		header, err := archive.Next()
		if err != nil || header.Name != strings.TrimPrefix(ociimage.RuntimePath, "/") || header.Mode != 0o755 {
			t.Fatalf("runtime layer: %#v %v", header, err)
		}
		binary, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		if config.Config.Labels["sh.holon.bridge.runtime"] != digest.FromBytes(binary).String() {
			t.Fatal("runtime provenance does not match bundled executable")
		}
		executable, err := elf.NewFile(bytes.NewReader(binary))
		if err != nil {
			t.Fatal(err)
		}
		machine := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[child.Platform.Architecture]
		if executable.Machine != machine {
			t.Fatalf("runtime architecture = %s", executable.Machine)
		}
		if _, err := archive.Next(); err != io.EOF {
			t.Fatalf("unexpected runtime files: %v", err)
		}
	}
}
