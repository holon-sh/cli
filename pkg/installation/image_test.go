package installation

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/holon-sh/protocol"

	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

type registryItem struct {
	mediaType string
	data      []byte
}

type registryCredential struct{ username, password string }

type imageRegistry struct {
	mu             sync.Mutex
	items          map[string]registryItem
	uploads        map[string][]byte
	credentials    map[string]registryCredential
	bearer         bool
	credentialLeak bool
}

func newImageRegistry() *imageRegistry {
	return &imageRegistry{
		items: map[string]registryItem{}, uploads: map[string][]byte{},
		credentials: map[string]registryCredential{"holon": {protocol.UploadUsername, "grant-secret"}},
	}
}

func (r *imageRegistry) authorize(w http.ResponseWriter, request *http.Request) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if request.URL.Path == "/token" {
		scope := strings.Split(request.URL.Query().Get("scope"), ":")
		if len(scope) != 3 || scope[0] != "repository" {
			w.WriteHeader(http.StatusBadRequest)
			return false
		}
		user, password, ok := request.BasicAuth()
		prefix, _, _ := strings.Cut(scope[1], "/")
		want := r.credentials[prefix]
		if !ok || want.username == "" || user != want.username || password != want.password {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		token := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
		json.NewEncoder(w).Encode(map[string]string{"token": token})
		return false
	}
	path := strings.TrimPrefix(request.URL.Path, "/v2/")
	repository, _, found := strings.Cut(path, "/blobs/")
	if !found {
		repository, _, _ = strings.Cut(path, "/manifests/")
	}
	prefix, _, _ := strings.Cut(repository, "/")
	want := r.credentials[prefix]
	if want.username == "" {
		return true
	}
	if r.bearer {
		if token, ok := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer "); ok {
			if token == base64.StdEncoding.EncodeToString([]byte(want.username+":"+want.password)) {
				return true
			}
			r.credentialLeak = true
		}
		actions := "pull"
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			actions += ",push"
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="https://%s/token",service="fixture",scope="repository:%s:%s"`,
			request.Host, repository, actions))
	} else {
		user, password, ok := request.BasicAuth()
		if ok && user == want.username && password == want.password {
			return true
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
	}
	w.WriteHeader(http.StatusUnauthorized)
	return false
}

func (r *imageRegistry) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if !r.authorize(w, request) {
		return
	}
	if request.URL.Path == "/v2/" {
		w.WriteHeader(http.StatusOK)
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/v2/")
	if strings.Contains(path, "/blobs/uploads/") || strings.HasSuffix(path, "/blobs/uploads") {
		r.upload(w, request)
		return
	}
	if request.Method == http.MethodPut && strings.Contains(path, "/manifests/") {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		digest := digest.FromBytes(body).String()
		repository, _, _ := strings.Cut(path, "/manifests/")
		item := registryItem{mediaType: request.Header.Get("Content-Type"), data: body}
		r.mu.Lock()
		r.items[path], r.items[repository+"/manifests/"+digest] = item, item
		r.mu.Unlock()
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)
		return
	}
	r.mu.Lock()
	item, ok := r.items[path]
	r.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", item.mediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(item.data)))
	w.Header().Set("Docker-Content-Digest", digest.FromBytes(item.data).String())
	if request.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(item.data)
}

func (r *imageRegistry) upload(w http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/v2/")
	repository, _, _ := strings.Cut(path, "/blobs/")
	if request.Method == http.MethodPost {
		r.mu.Lock()
		if source := request.URL.Query().Get("from"); source != "" {
			digest := request.URL.Query().Get("mount")
			if item, ok := r.items[source+"/blobs/"+digest]; ok {
				r.items[repository+"/blobs/"+digest] = item
				r.mu.Unlock()
				w.Header().Set("Docker-Content-Digest", digest)
				w.Header().Set("Location", "/v2/"+repository+"/blobs/"+digest)
				w.WriteHeader(http.StatusCreated)
				return
			}
		}
		id := strconv.Itoa(len(r.uploads))
		path = repository + "/blobs/uploads/" + id
		r.uploads[path] = nil
		r.mu.Unlock()
		w.Header().Set("Location", "/v2/"+path)
		w.Header().Set("Docker-Upload-Uuid", id)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if request.Method == http.MethodPatch || request.Method == http.MethodPut {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.uploads[path] = append(r.uploads[path], body...)
		data := r.uploads[path]
		if request.Method == http.MethodPut {
			r.items[repository+"/blobs/"+request.URL.Query().Get("digest")] = registryItem{data: data}
		}
		r.mu.Unlock()
		w.Header().Set("Location", "/v2/"+path)
		if request.Method == http.MethodPut {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func addTestIndex(registry *imageRegistry, annotations map[string]string, children ...ocispec.Descriptor) ocispec.Descriptor {
	data, _ := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: children, Annotations: annotations,
	})
	descriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, data)
	registry.items["source/manifests/"+descriptor.Digest.String()] = registryItem{mediaType: descriptor.MediaType, data: data}
	return descriptor
}

func addTestImage(t *testing.T, registry *imageRegistry, architecture string, config ocispec.ImageConfig, files map[string][]byte) ocispec.Descriptor {
	t.Helper()
	var raw bytes.Buffer
	archive := tar.NewWriter(&raw)
	archiveFiles := fstest.MapFS{}
	for name, data := range files {
		archiveFiles[name] = &fstest.MapFile{Data: data, Mode: 0o755}
	}
	if err := archive.AddFS(archiveFiles); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	layer := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, raw.Bytes())
	registry.items["source/blobs/"+layer.Digest.String()] = registryItem{data: raw.Bytes()}
	configuration, _ := json.Marshal(ocispec.Image{
		Platform: ocispec.Platform{OS: "linux", Architecture: architecture}, Config: config,
		RootFS:  ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{layer.Digest}},
		History: []ocispec.History{{CreatedBy: "original image"}},
	})
	configDescriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, configuration)
	registry.items["source/blobs/"+configDescriptor.Digest.String()] = registryItem{data: configuration}
	manifest, _ := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest,
		Config: configDescriptor, Layers: []ocispec.Descriptor{layer}, Annotations: map[string]string{"example": "preserved"},
	})
	descriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifest)
	registry.items["source/manifests/"+descriptor.Digest.String()] = registryItem{mediaType: descriptor.MediaType, data: manifest}
	descriptor.Platform = &ocispec.Platform{OS: "linux", Architecture: architecture}
	return descriptor
}

func TestPrebuiltImageImports(t *testing.T) {
	for _, scenario := range []struct {
		name, sourcePassword, grant string
		denied                      bool
	}{
		{name: "private source", sourcePassword: "source-secret", grant: "grant-secret"},
		{name: "invalid upload grant", grant: "wrong-grant", denied: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newImageRegistry()
			fixture.bearer = true
			children := []ocispec.Descriptor{}
			for _, architecture := range []string{"amd64", "arm64"} {
				children = append(children, addTestImage(t, fixture, architecture, ocispec.ImageConfig{}, nil))
			}
			indexDigest := addTestIndex(fixture, nil, children...).Digest
			server := httptest.NewTLSServer(fixture)
			defer server.Close()
			host := strings.TrimPrefix(server.URL, "https://")
			configDir := t.TempDir()
			t.Setenv("DOCKER_CONFIG", configDir)
			auths := map[string]map[string]string{}
			grant := protocol.UploadGrant{Registry: host, Grant: scenario.grant, Repositories: map[string]string{"echo": "holon/upload/echo"}}
			manifest := manifestSpec{Apps: []manifestApp{{Name: "echo", resolved: resolvedApp{App: protocol.App{
				Name: "echo", Port: 3000, Image: host + "/source@" + indexDigest.String(),
			}}}}}
			options := buildOptions{HTTPClient: server.Client()}
			renewals := 0
			upload := imageUpload{grant: grant, load: func(context.Context) (protocol.UploadGrant, error) {
				renewals++
				return grant, nil
			}}
			passwords := []string{scenario.sourcePassword}
			if scenario.sourcePassword != "" {
				passwords = append(passwords, "replacement-source-secret")
			}
			for _, password := range passwords {
				if password != "" {
					fixture.mu.Lock()
					fixture.credentials["source"] = registryCredential{"reader", password}
					fixture.mu.Unlock()
					auths[host] = map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("reader:" + password))}
				}
				data, _ := json.Marshal(map[string]any{"auths": auths})
				if err := os.WriteFile(filepath.Join(configDir, "config.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
				apps, err := buildAndPush(t.Context(), manifest, &upload, options)
				if scenario.denied {
					var response *errcode.ErrorResponse
					if !errors.As(err, &response) || response.StatusCode != http.StatusUnauthorized || len(apps) != 0 || renewals != 1 {
						t.Fatalf("invalid upload grant: apps=%v, renewals=%d, error=%v", apps, renewals, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := host + "/holon/upload/echo@" + indexDigest.String()
				if len(apps) != 1 || apps[0].Image != want {
					t.Fatalf("imported apps = %#v, want %s", apps, want)
				}
				fixture.mu.Lock()
				leaked := fixture.credentialLeak
				fixture.mu.Unlock()
				if leaked {
					t.Fatal("credentials crossed source and destination boundaries")
				}
			}
		})
	}
}
