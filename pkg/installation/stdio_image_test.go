package installation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"

	"github.com/holon-sh/cli/pkg/ociimage"
	"github.com/holon-sh/protocol"
)

func preparedConfig(t *testing.T, registry *imageRegistry, image string) (ocispec.Manifest, ocispec.Image) {
	t.Helper()
	repository, reference, _ := strings.Cut(image, "@")
	_, repository, _ = strings.Cut(repository, "/")
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
	stdio := []string{ociimage.RuntimePath, "--origin", "https://holon.example", "--"}
	for _, test := range []struct {
		name                      string
		entrypoint, cmd, expected []string
		files                     map[string][]byte
		wantErr                   string
	}{
		{
			name: "entrypoint and cmd", entrypoint: []string{"/server", "--flag"}, cmd: []string{"default"},
			expected: slices.Concat(stdio, []string{"/server", "--flag"}),
		},
		{name: "cmd only", cmd: []string{"/server", "arg"}, expected: stdio},
		{name: "missing executable", wantErr: "no executable"},
		{
			name: "reserved file", cmd: []string{"/server"},
			files: map[string][]byte{"holon-mcp-runtime": []byte("collision")}, wantErr: "reserved path",
		},
		{
			name: "reserved directory", cmd: []string{"/server"},
			files: map[string][]byte{"holon-mcp-runtime/file": []byte("collision")}, wantErr: "reserved path",
		},
		{
			name: "reserved whiteout", cmd: []string{"/server"},
			files: map[string][]byte{".wh.holon-mcp-runtime": []byte("collision")}, wantErr: "reserved path",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry, server := buildRegistry(t)
			host := strings.TrimPrefix(server.URL, "https://")
			source := addTestImage(t, registry, "amd64", ocispec.ImageConfig{
				Entrypoint: test.entrypoint, Cmd: test.cmd,
				Env: []string{"PATH=/bin"}, WorkingDir: "/work", User: "10001:10001",
				Labels: map[string]string{"custom": "label"}, ExposedPorts: map[string]struct{}{"9000/tcp": {}}, StopSignal: "SIGQUIT",
			}, test.files)
			image := host + "/source@" + source.Digest.String()
			grant := protocol.UploadGrant{Registry: host, Grant: "grant-secret", Repositories: map[string]string{"app": "holon/upload/app"}}
			manifest := manifestSpec{Apps: []manifestApp{{
				Name: "app", resolved: resolvedApp{stdio: true, App: protocol.App{Name: "app", Image: image, Port: 3000}},
			}}}
			options := buildOptions{HTTPClient: server.Client(), origin: "https://holon.example", Env: []string{"DOCKER_CONFIG=" + t.TempDir()}}
			apps, err := buildAndPush(t.Context(), manifest, &imageUpload{manifest: manifest, grant: grant}, options)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("want %s: %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			originalManifest, original := preparedConfig(t, registry, image)
			imageManifest, config := preparedConfig(t, registry, apps[0].Image)
			want := original.Config
			want.Entrypoint, want.StopSignal = test.expected, "SIGTERM"
			want.Labels["sh.holon.bridge.source"] = host + "/holon/upload/app@" + source.Digest.String()
			delete(config.Config.Labels, "sh.holon.bridge.runtime")
			if diff := cmp.Diff(want, config.Config); diff != "" {
				t.Fatalf("image configuration (-want +got):\n%s", diff)
			}
			if len(imageManifest.Layers) != 2 || !cmp.Equal(imageManifest.Layers[:1], originalManifest.Layers) ||
				imageManifest.Annotations["example"] != "preserved" || len(config.RootFS.DiffIDs) != 2 || len(config.History) != 2 {
				t.Fatalf("layers or metadata lost: %#v %#v", imageManifest, config)
			}
		})
	}
	t.Run("layer integrity", func(t *testing.T) {
		checkedLayers.Clear()
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
		options := buildOptions{HTTPClient: server.Client()}
		prepare := func(descriptor ocispec.Descriptor, data []byte) error {
			manifest.Layers = []ocispec.Descriptor{descriptor}
			encoded, _ := json.Marshal(manifest)
			image := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, encoded)
			registry.mu.Lock()
			registry.items["source/manifests/"+image.Digest.String()] = registryItem{mediaType: image.MediaType, data: encoded}
			registry.items["source/blobs/"+descriptor.Digest.String()] = registryItem{mediaType: descriptor.MediaType, data: data}
			registry.mu.Unlock()
			_, err := prepareStdioImage(t.Context(), repository+"@"+image.Digest.String(), "", options)
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
					if err := prepare(descriptor, corrupt); err == nil {
						t.Fatal("invalid layer accepted")
					}
				}
				if err := prepare(descriptor, layer.data); err != nil {
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
			if err := prepare(changed, raw); err == nil {
				t.Fatal("changed descriptor reused a successful scan")
			}
		}
	})
}

func TestStdioMultiArchitectureBuild(t *testing.T) {
	registry, server := buildRegistry(t)
	var children []ocispec.Descriptor
	for _, architecture := range []string{"amd64", "arm64"} {
		children = append(children, addTestImage(t, registry, architecture, ocispec.ImageConfig{Cmd: []string{"/server"}}, nil))
	}
	attestation := addTestImage(t, registry, "unknown", ocispec.ImageConfig{}, nil)
	attestation.Platform = &ocispec.Platform{OS: "unknown", Architecture: "unknown"}
	attestation.Annotations = map[string]string{"vnd.docker.reference.type": "attestation-manifest"}
	children = append(children, attestation)
	descriptor := addTestIndex(registry, children...)
	host := strings.TrimPrefix(server.URL, "https://")
	grant := protocol.UploadGrant{Registry: host, Grant: "grant-secret", Repositories: map[string]string{"echo": "holon/upload/echo"}}
	options := buildOptions{HTTPClient: server.Client(), Env: []string{"DOCKER_CONFIG=" + t.TempDir()}}
	source := host + "/source@" + descriptor.Digest.String()
	app := manifestApp{Name: "echo", resolved: resolvedApp{stdio: true, App: protocol.App{Name: "echo", Image: source, Port: 3000}}}
	prepared, err := buildAndPush(t.Context(), manifestSpec{Apps: []manifestApp{app}}, &imageUpload{grant: grant}, options)
	if err != nil {
		t.Fatal(err)
	}
	var result ocispec.Index
	_, reference, _ := strings.Cut(prepared[0].Image, "@")
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
