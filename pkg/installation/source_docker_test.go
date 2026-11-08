//go:build docker

package installation

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
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
	for _, value := range []string{"first", "other"} {
		source := staticfs.NewFS()
		source.Add("value", &fstypes.Stat{Mode: 0o644}, []byte(value))
		request := buildRequest{
			app: manifestApp{Name: "alpha", Path: value}, upload: &imageUpload{grant: grant},
			Env: options.Env, httpClient: options.HTTPClient, builders: options.builders,
		}
		descriptor, err := publishPlatform(t.Context(), "linux/"+runtime.GOARCH, request, source, "FROM scratch\nCOPY value /value\nENV RECIPE="+value+"\n", io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		var manifest ocispec.Manifest
		var config ocispec.Image
		publishedJSON(t, repository, descriptor, &manifest)
		publishedJSON(t, repository, manifest.Config, &config)
		if !slices.Contains(config.Config.Env, "RECIPE="+value) {
			t.Fatalf("stale Dockerfile: %v", config.Config.Env)
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
		if _, err := archive.Next(); err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(archive)
		decoder.Close()
		if err != nil || string(data) != value {
			t.Fatalf("%s source = %q, want %q: %v", value, data, value, err)
		}
	}
}

func TestDockerSourceBuild(t *testing.T) {
	root := t.TempDir()
	manifest := buildManifest(t, root)
	grant, options := sourceRegistry(t)
	options.architectures = []string{runtime.GOARCH}
	upload := &imageUpload{grant: grant}

	t.Run("shared layers", func(t *testing.T) {
		beta := buildManifest(t, t.TempDir()).Apps[0]
		beta.Name, beta.resolved.App.Name = "beta", "beta"
		shared := manifestSpec{Apps: []manifestApp{manifest.Apps[0], beta}}
		sharedGrant := grant
		sharedGrant.Repositories = map[string]string{"alpha": "apps/alpha", "beta": "apps/beta"}
		var mu sync.Mutex
		uploads := map[string]int{}
		observed := options
		observed.HTTPClient = installationtest.HTTPClient(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/blobs/uploads/") {
				mu.Lock()
				uploads[r.URL.Query().Get("digest")]++
				mu.Unlock()
			}
			return options.HTTPClient.Transport.RoundTrip(r)
		})
		apps, err := buildAndPush(t.Context(), prepareBuild(t, shared), &imageUpload{grant: sharedGrant}, observed)
		if err != nil {
			t.Fatal(err)
		}
		for _, app := range apps {
			repository, err := imageRepository(grant.Registry+"/"+sharedGrant.Repositories[app.Name], grant.Grant, options.HTTPClient)
			if err != nil {
				t.Fatal(err)
			}
			descriptor, err := repository.Resolve(t.Context(), app.Image)
			if err != nil {
				t.Fatal(err)
			}
			var index ocispec.Index
			publishedJSON(t, repository, descriptor, &index)
			for _, platform := range index.Manifests {
				if platform.Platform == nil {
					continue
				}
				var image ocispec.Manifest
				publishedJSON(t, repository, platform, &image)
				for _, layer := range image.Layers {
					if count := uploads[layer.Digest.String()]; count != 1 {
						t.Fatalf("shared layer %s uploaded %d times, want once", layer.Digest, count)
					}
				}
			}
		}
	})

	t.Run("progress failure", func(t *testing.T) {
		want := errors.New("progress writer failed")
		failed := options
		failed.Stderr = progressFunc(func([]byte) (int, error) { return 0, want })
		if _, err := buildSource(t.Context(), prepareBuild(t, manifest).Apps[0], upload, failed, nil); !errors.Is(err, want) {
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
		if _, err := buildAndPush(ctx, prepareBuild(t, manifest), upload, canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled build: %v", err)
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
