//go:build docker

package installation

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/holon-sh/protocol"
	"github.com/klauspost/compress/zstd"
	"github.com/moby/buildkit/util/staticfs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	fstypes "github.com/tonistiigi/fsutil/types"
	"oras.land/oras-go/v2/content"
)

func TestDockerSourceCacheIsolation(t *testing.T) {
	grant, options := sourceRegistry(t)
	repository, err := imageRepository(grant.Registry+"/"+grant.Repositories["alpha"], grant.Grant, options.HTTPClient)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ project, value, recipe string }{
		{"first", "first", "one"},
		{"other", "other", "two"},
		{"first", "first", "two"},
	} {
		source, recipe := staticfs.NewFS(), staticfs.NewFS()
		source.Add("value", &fstypes.Stat{Mode: 0o644}, []byte(test.value))
		recipe.Add("Dockerfile", &fstypes.Stat{Mode: 0o600}, []byte("FROM scratch\nCOPY value /value\nENV RECIPE="+test.recipe+"\n"))
		request := buildRequest{
			app: manifestApp{Name: "alpha", Path: test.project}, upload: &imageUpload{grant: grant},
			Env: options.Env, httpClient: options.HTTPClient, builders: options.builders,
		}
		descriptor, err := publishPlatform(t.Context(), "linux/"+runtime.GOARCH, request, source, recipe, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		var manifest ocispec.Manifest
		var config ocispec.Image
		publishedJSON(t, repository, descriptor, &manifest)
		publishedJSON(t, repository, manifest.Config, &config)
		if !slices.Contains(config.Config.Env, "RECIPE="+test.recipe) {
			t.Fatalf("stale Dockerfile: %v", config.Config.Env)
		}
		if len(manifest.Layers) != 1 {
			t.Fatalf("expected one source layer, got %d", len(manifest.Layers))
		}
		data, err := content.FetchAll(t.Context(), repository.Blobs(), manifest.Layers[0])
		if err != nil {
			t.Fatal(err)
		}
		decoder, err := zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		archive := tar.NewReader(decoder)
		header, err := archive.Next()
		if err != nil || header.Name != "value" {
			t.Fatalf("unexpected source layer: %v %v", header, err)
		}
		value, err := io.ReadAll(archive)
		decoder.Close()
		if err != nil || string(value) != test.value {
			t.Fatalf("%s source = %q, want %q: %v", test.project, value, test.value, err)
		}
	}
}

func TestDockerSourceBuildFailures(t *testing.T) {
	root := t.TempDir()
	manifest := buildManifest(t, root)
	grant, options := sourceRegistry(t)
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
		apps, err := buildAndPush(t.Context(), prepareBuild(t, manifest), denied, options)
		if err == nil || len(apps) != 0 || calls != 2 {
			t.Fatalf("denied grant: apps=%v, grants=%d: %v", apps, calls, err)
		}
	})
	t.Run("progress failure", func(t *testing.T) {
		want := errors.New("progress writer failed")
		failed := options
		failed.Stderr = progressFunc(func([]byte) (int, error) { return 0, want })
		if _, err := buildSource(t.Context(), prepareBuild(t, manifest).Apps[0], upload, failed); !errors.Is(err, want) {
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
		canceled.Stderr = progressFunc(func(data []byte) (int, error) {
			if strings.Contains(string(data), "cancel-build") {
				cancel()
			}
			return len(data), nil
		})
		if apps, err := buildAndPush(ctx, prepareBuild(t, manifest), upload, canceled); !errors.Is(err, context.Canceled) || len(apps) != 0 {
			t.Fatalf("canceled build: %v: %v", apps, err)
		}
	})
}

func sourceRegistry(t *testing.T) (protocol.UploadGrant, buildOptions) {
	t.Helper()
	registry, server := buildRegistry(t)
	registry.bearer = true
	registry.credentials["apps"] = registryCredential{protocol.UploadUsername, "grant-secret"}
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
	return protocol.UploadGrant{Grant: "grant-secret", Registry: strings.TrimPrefix(server.URL, "https://"), Repositories: map[string]string{"alpha": "apps/alpha"}}, buildOptions{
		Env: append(os.Environ(), "BUILDX_BUILDER="+name), Stderr: os.Stderr, HTTPClient: server.Client(), builders: builders,
	}
}
