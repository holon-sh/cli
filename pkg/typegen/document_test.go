package typegen

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
)

func TestDocumentBytesAndValidation(t *testing.T) {
	const original = ` { "schema": { "tools": [{"name":"lookup","inputSchema":{"type":"object"}}]}, "identity": {"version":"1", "name":"example/orders"} } `
	document, err := ParseDocument(t.Context(), []byte(original))
	if err != nil {
		t.Fatal(err)
	}
	if string(document.Data) != original || document.Digest != digest.FromString(original).String() {
		t.Fatal("document bytes were rewritten")
	}
	for _, invalid := range []string{
		`{"version":1,"identity":{"name":"a","version":"1"},"contract":{"tools":[]}}`,
		`{"schema":{"tools":[]}}`,
		`{"identity":{"name":"a","version":"1"},"requires":{"orders":"sha256:abc"}}`,
	} {
		if _, err := ParseDocument(t.Context(), []byte(invalid)); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}

func TestPrepareResolvesRegistryPackages(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	docker := func(password string) string {
		dir := filepath.Join(t.TempDir(), ".docker")
		put(t, dir, "config.json", `{"auths":{"images.example":{"auth":"`+base64.StdEncoding.EncodeToString([]byte("user:"+password))+`"}}}`)
		return dir
	}
	// The registry's blob need not match our writer's bytes; a consumer pins the blob as fetched.
	original := append([]byte(" "), mustJSON(t, ordersMetadata())...)
	corrupt := imageClient(t, ordersMetadata())
	served := corrupt.Transport
	corrupt.Transport = registryResponse(func(request *http.Request) (*http.Response, error) {
		response, err := served.RoundTrip(request)
		if err == nil && strings.Contains(request.URL.Path, "/blobs/") {
			response.Body.Close()
			response.Body = io.NopCloser(bytes.NewReader(bytes.Replace(original, []byte("lookup"), []byte("broken"), 1)))
		}
		return response, err
	})
	for _, test := range []struct {
		name  string
		image *http.Client
		env   []string
		want  string
	}{
		{name: "docker config", env: []string{"DOCKER_CONFIG=" + docker("secret")}},
		{name: "docker under HOME", env: []string{"HOME=" + filepath.Dir(docker("secret")), "DOCKER_CONFIG="}},
		{name: "rejected docker credential", env: []string{"DOCKER_CONFIG=" + docker("wrong")}, want: "images.example/orders:1.0.0"},
		{name: "corrupt blob", image: corrupt, want: "failed verification"},
	} {
		t.Run(test.name, func(t *testing.T) {
			image := test.image
			if image == nil {
				image = authenticatedRegistry(t, ordersMetadata())
			}
			source, client := registryImageSource(t, image)
			c := Consumer{Name: "caller", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: []Source{source}}
			prepared, err := Prepare(t.Context(), []Consumer{c}, Options{HTTPClient: client, Env: append([]string{"DOCKER_CONFIG=" + t.TempDir()}, test.env...)})
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("Prepare = %v, want %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			reference := prepared.Metadata()["caller"].Requires["orders"]
			if reference != digest.FromBytes(original).String() || !bytes.Equal(prepared.Documents()[reference].Data, original) {
				t.Fatal("dependency was not pinned to the registry's blob bytes")
			}
		})
	}
}

func TestLocalDependencyDocumentsAreComplete(t *testing.T) {
	checkout, extract := checkoutConsumer(t, TargetPy)
	orders := Consumer{Name: "orders", Dir: checkout.Sources[0].Path, Server: &Source{Path: checkout.Sources[0].Path}, Targets: []string{TargetPy}, Sources: []Source{{Alias: "inventory", Document: document(t, ordersMetadata())}}}
	prepared, err := Prepare(t.Context(), []Consumer{checkout, orders}, Options{Run: extract})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	reference := prepared.Metadata()["checkout"].Requires["orders"]
	document := prepared.Documents()[reference]
	if document == nil || len(document.Requires) != 1 || document.Requires["inventory"] == "" {
		t.Fatalf("provider's own dependencies lost: %+v", document)
	}
	own, err := NewDocument(t.Context(), prepared.Metadata()["orders"])
	if err != nil || own.Digest != reference {
		t.Fatalf("consumer did not pin provider's published document: %v", err)
	}
	orders.Sources = []Source{{Path: checkout.Dir}}
	if p, err := Prepare(t.Context(), []Consumer{checkout, orders}, Options{Run: extract}); err == nil || !strings.Contains(err.Error(), "contract dependency cycle") {
		if p != nil {
			p.Close()
		}
		t.Fatalf("cyclic document graph: %v", err)
	}
}
