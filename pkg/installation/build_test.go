package installation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"

	"github.com/holon-sh/cli/internal/testfiles"
	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
	"github.com/holon-sh/protocol/runtimecontract"
)

func buildRegistry(t *testing.T) (*imageRegistry, *httptest.Server) {
	t.Helper()
	fixture := newImageRegistry()
	server := httptest.NewTLSServer(fixture)
	t.Cleanup(server.Close)
	return fixture, server
}

func buildManifest(t *testing.T, root string) manifestSpec {
	t.Helper()
	sourceFiles(t, root, map[string]string{"package.json": sourcePackage, "server.js": "console.log(1)"})
	manifest := manifestSpec{
		Apps: []manifestApp{
			{
				Name: "alpha",
				Path: root,
				Access: &protocol.AppAccess{
					Server: &protocol.AccessRule{},
				},
			},
		},
	}
	resolved, err := resolveManifestGraph(t.Context(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func buildGrant(server *httptest.Server) protocol.UploadGrant {
	return protocol.UploadGrant{Grant: "grant-secret", Registry: strings.TrimPrefix(server.URL, "https://"), Repositories: map[string]string{"alpha": "holon/upload/alpha"}}
}

var sourceFiles = testfiles.Write[string]

const sourcePackage = `{"private":true,"engines":{"node":">=22.20.0 <23"}}`

const buildSessionName = "build-session"

const baseRepository = "registry.example/source"

type buildCall struct {
	app   string
	plan  runtimecontract.Plan
	build runtimecontract.Build
	files map[string][]byte
}

type buildFixture struct {
	mu       sync.Mutex
	base     digest.Digest
	failure  func(buildCall) string
	ran      func(buildCall) runtimecontract.Ran
	during   func(buildCall)
	apps     []protocol.App
	sessions int
	deleted  int
	calls    []buildCall
}

func serveBuilds(t *testing.T, h *runHarness) *buildFixture {
	t.Helper()
	f := &buildFixture{base: addTestBase(t, h.registry, "development")}
	session := protocol.SessionResponse{
		Name: buildSessionName, UID: "build-uid", Revision: "1", Conditions: []protocol.Condition{{Type: protocol.ConditionReady, Status: "True"}},
	}
	h.control.Handle([]string{protocol.ToolCreateSession}, nil, func(w http.ResponseWriter, r *http.Request) {
		installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
		var request protocol.AppsRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		f.mu.Lock()
		f.apps = request.Apps
		f.sessions++
		f.mu.Unlock()
		installationtest.WriteJSON(w, session)
	})
	h.control.Handle([]string{protocol.ToolGetSession, protocol.ToolDeleteSession}, map[string]string{"session": buildSessionName},
		func(w http.ResponseWriter, r *http.Request) {
			if installationtest.ToolName(r) == protocol.ToolGetSession {
				installationtest.WriteJSON(w, session)
				return
			}
			f.mu.Lock()
			f.deleted++
			f.mu.Unlock()
			installationtest.WriteJSON(w, protocol.DeleteSessionResult{Deleted: true})
		})
	h.mux.HandleFunc("POST /sessions/{id}/apps/{app}/build", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || r.PathValue("id") != buildSessionName {
			t.Errorf("build request for %s: %v", r.PathValue("id"), err)
			return
		}
		call := buildCall{app: r.PathValue("app"), files: overlayEntries(t, body)}
		if err := errors.Join(json.Unmarshal(call.files[runtimecontract.PlanFile], &call.plan),
			json.Unmarshal(call.files[runtimecontract.BuildFile], &call.build)); err != nil {
			t.Error(err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, call)
		failure, ran, during, base := f.failure, f.ran, f.during, f.base
		f.mu.Unlock()
		if during != nil {
			during(call)
		}
		if failure != nil {
			if body := failure(call); body != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, body)
				return
			}
		}
		var reply runtimecontract.BuildReply
		if len(call.build.Run) != 0 {
			reply.Ran = &runtimecontract.Ran{}
			if slices.Contains(call.build.Run, "contract") {
				reply.Ran.Stdout = servedContract
			}
			if ran != nil {
				*reply.Ran = ran(call)
			}
		}
		if call.build.Freeze != nil {
			reply.Image = h.registry.addFrozenImage(call.app, base).String()
		}
		installationtest.WriteJSON(w, reply)
	})
	return f
}

func (r *imageRegistry) addFrozenImage(app string, base digest.Digest) digest.Digest {
	image := ocispec.Image{Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"}, Config: ocispec.ImageConfig{WorkingDir: "/app/source"}}
	var layers []ocispec.Descriptor
	for _, layer := range []string{"dependencies", "source"} {
		data := []byte(layer + " of " + app)
		layers = append(layers, ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayerZstd, Digest: digest.FromBytes(data), Size: int64(len(data))})
		image.RootFS.DiffIDs = append(image.RootFS.DiffIDs, digest.FromString("tar "+string(data)))
	}
	config, _ := json.Marshal(image)
	configDescriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, config)
	manifest, _ := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: configDescriptor, Layers: layers,
		Annotations: map[string]string{ocispec.AnnotationBaseImageDigest: base.String()},
	})
	manifestDigest := digest.FromBytes(manifest)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items["holon/upload/"+app+"/blobs/"+configDescriptor.Digest.String()] = registryItem{data: config}
	r.items["holon/upload/"+app+"/manifests/"+manifestDigest.String()] = registryItem{mediaType: ocispec.MediaTypeImageManifest, data: manifest}
	return manifestDigest
}

func addTestBase(t *testing.T, registry *imageRegistry, label string) digest.Digest {
	t.Helper()
	child := addTestImage(t, registry, "amd64", ocispec.ImageConfig{
		User: "10001:10001", WorkingDir: "/app", Entrypoint: []string{"/holon-mcp-runtime"},
		Env:    []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/app", "HOLON_RELOAD_DIR=/app/source"},
		Labels: map[string]string{"base": label},
	}, map[string][]byte{"holon-mcp-runtime": []byte("runtime")})
	return addTestIndex(registry, child).Digest
}

func assembledImage(t *testing.T, registry *imageRegistry, image string) (ocispec.Index, ocispec.Manifest, ocispec.Image) {
	t.Helper()
	repository, reference, _ := strings.Cut(image, "@")
	var index ocispec.Index
	registry.mu.Lock()
	err := json.Unmarshal(registry.items[strings.TrimPrefix(repository, "registry.example/")+"/manifests/"+reference].data, &index)
	registry.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	manifest, config := preparedConfig(t, registry, repository+"@"+index.Manifests[0].Digest.String())
	return index, manifest, config
}

func TestSourceRejectsEscapingSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	manifest := buildManifest(t, root)
	sourceFiles(t, outside, map[string]string{"file": ""})
	if err := os.Symlink(filepath.Join(outside, "file"), filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareManifestTypes(t.Context(), manifest, buildOptions{}, nil); err == nil || !strings.Contains(err.Error(), "symlink escapes application") {
		t.Fatalf("escaping symlink: %v", err)
	}
}
