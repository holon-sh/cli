package installation

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	containercontent "github.com/containerd/containerd/v2/core/content"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
)

func TestSharedSourceLayers(t *testing.T) {
	for _, scenario := range []struct {
		name                   string
		warm, removed, renewed bool
		uploads                int
	}{
		{name: "concurrent apps", uploads: 1},
		{name: "warm app with renewed grant", warm: true, renewed: true},
		{name: "removed source", warm: true, removed: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				registry := newImageRegistry()
				registry.bearer = true
				var tokens atomic.Int32
				var uploaded atomic.Int64
				var pending chan struct{}
				client := installationtest.HTTPClient(func(r *http.Request) (*http.Response, error) {
					r = r.Clone(r.Context())
					if r.URL.Path == "/token" {
						tokens.Add(1)
					}
					if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/blobs/uploads/") {
						uploaded.Add(r.ContentLength)
						if pending != nil {
							<-pending
						}
					}
					response := httptest.NewRecorder()
					registry.ServeHTTP(response, r)
					result := response.Result()
					result.Request = r
					return result, nil
				})
				grant := protocol.UploadGrant{Registry: "registry.example", Grant: "grant-secret",
					Repositories: map[string]string{"alpha": "holon/alpha", "beta": "holon/beta"}}
				upload := &imageUpload{grant: grant, load: func(context.Context) (protocol.UploadGrant, error) { return grant, nil }}
				data := []byte("shared application layer")
				desc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, data)
				source := memory.New()
				if err := source.Push(t.Context(), desc, bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
				if scenario.warm {
					repository, err := imageRepository(grant.Registry+"/"+grant.Repositories["alpha"], grant.Grant, client)
					if err != nil {
						t.Fatal(err)
					}
					if err := repository.Push(t.Context(), desc, bytes.NewReader(data)); err != nil {
						t.Fatal(err)
					}
					tokens.Store(0)
					export := sourceExport{request: buildRequest{app: manifestApp{Name: "alpha"}, upload: upload, httpClient: client}}
					for range 2 {
						if _, err := export.Writer(t.Context(), containercontent.WithDescriptor(desc)); !errors.Is(err, cerrdefs.ErrAlreadyExists) {
							t.Fatalf("existing source layer: %v", err)
						}
					}
					if tokens.Load() != 1 {
						t.Fatalf("token exchanges for warm source checks: %d, want one", tokens.Load())
					}
					uploaded.Store(0)
					if scenario.removed {
						registry.mu.Lock()
						delete(registry.items, "holon/alpha/blobs/"+desc.Digest.String())
						registry.mu.Unlock()
					}
				}
				if scenario.renewed {
					grant.Grant = "renewed-grant"
					registry.credentials["holon"] = registryCredential{protocol.UploadUsername, grant.Grant}
				}
				pending = make(chan struct{})
				var group errgroup.Group
				for name := range grant.Repositories {
					request := buildRequest{app: manifestApp{Name: name}, upload: upload, httpClient: client}
					group.Go(func() error {
						return request.publish(t.Context(), func(repo *remote.Repository, _ protocol.UploadGrant) error {
							return upload.pushLayer(t.Context(), repo, desc, source)
						})
					})
				}
				synctest.Wait()
				close(pending)
				if err := group.Wait(); err != nil {
					t.Fatal(err)
				}
				for _, name := range grant.Repositories {
					repo, err := imageRepository(grant.Registry+"/"+name, grant.Grant, client)
					if err != nil {
						t.Fatal(err)
					}
					got, err := content.FetchAll(t.Context(), repo, desc)
					if err != nil || !bytes.Equal(got, data) {
						t.Fatalf("published layer in %s: %q, %v", repo.Reference.Repository, got, err)
					}
				}
				if !scenario.removed && uploaded.Load() != int64(scenario.uploads*len(data)) {
					t.Fatalf("uploaded %d bytes for %s", uploaded.Load(), scenario.name)
				}
			})
		})
	}
}

func TestSharedSourceUploadCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	registry := newImageRegistry()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			close(started)
			<-release
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		registry.ServeHTTP(w, r)
	}))
	defer server.Close()
	defer close(release)
	repo, err := imageRepository(strings.TrimPrefix(server.URL, "https://")+"/holon/alpha", "grant-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	desc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, []byte("data"))
	source := memory.New()
	if err := source.Push(t.Context(), desc, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	upload := new(imageUpload)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = upload.pushLayer(ctx, repo, desc, source) }()
	<-started
	waiter, stop := context.WithCancel(t.Context())
	stop()
	if err := upload.pushLayer(waiter, repo, desc, source); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter: %v", err)
	}
}
