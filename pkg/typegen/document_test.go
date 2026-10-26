package typegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"oras.land/oras-go/v2/content"
)

func TestDocumentBytesAndValidation(t *testing.T) {
	const original = ` { "schema": { "tools": [{"name":"lookup","inputSchema":{"type":"object","properties":{"id":{"const":9007199254740993}}}}]}, "identity": {"version":"1", "name":"example/orders"} } `
	document, err := ParseDocument(t.Context(), []byte(original))
	if err != nil {
		t.Fatal(err)
	}
	if string(document.Data) != original || document.Digest != digest.FromString(original).String() {
		t.Fatal("document bytes were rewritten")
	}
	canonical, err := NewDocument(t.Context(), document.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"identity":{"name":"example/orders","version":"1"},"schema":{"tools":[{"inputSchema":{"properties":{"id":{"const":9007199254740993}},"type":"object"},"name":"lookup"}]}}`
	if string(canonical.Data) != want {
		t.Fatalf("writer bytes = %s", canonical.Data)
	}
	for _, invalid := range []string{
		`{"version":1,"identity":{"name":"a","version":"1"},"contract":{"tools":[]}}`,
		`{"identity":{"name":"a","version":"1"},"schema":null}`,
		`{"identity":{"name":"a","version":"1"},"requires":{"orders":{"identity":{"name":"b","version":"1"},"contract":{"tools":[]}}}}`,
		`{"identity":{"name":"a","version":"1"},"requires":{"orders":"https://example.com/contract"}}`,
		`{"identity":{"name":"a","version":"1"},"requires":{"orders":"sha256:abc"}}`,
	} {
		if _, err := ParseDocument(t.Context(), []byte(invalid)); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}

func TestImageDocumentReadAndCopy(t *testing.T) {
	// The fixture's struct JSON has a different key order from the writer's canonical
	// JSON. A consumer must pin and copy those original bytes, without re-encoding.
	original := mustJSON(t, ordersMetadata())
	for _, test := range []struct {
		name string
		data []byte
		want error
	}{
		{"preserved", original, nil},
		{"corrupt", bytes.Replace(original, []byte("lookup"), []byte("broken"), 1), content.ErrMismatchedDigest},
		{"truncated", original[:len(original)-1], io.ErrUnexpectedEOF},
		{"trailing", append(bytes.Clone(original), ' '), content.ErrTrailingData},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := imageClient(t, ordersMetadata(), "")
			transport := client.Transport
			client.Transport = registryResponse(func(request *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(request)
				if err == nil && strings.Contains(request.URL.Path, "/blobs/") {
					response.Body.Close()
					response.Body = io.NopCloser(bytes.NewReader(test.data))
				}
				return response, err
			})
			c := Consumer{Name: "checkout", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: []Source{{Image: "images.example/orders:1.0.0"}}}
			prepared, err := Prepare(t.Context(), []Consumer{c}, Options{HTTPClient: client, Env: []string{"DOCKER_CONFIG=" + t.TempDir()}})
			if !errors.Is(err, test.want) {
				t.Fatalf("Prepare = %v, want %v", err, test.want)
			}
			if err != nil {
				return
			}
			defer prepared.Close()
			reference := prepared.Metadata()["checkout"].Requires["orders"]
			if reference != digest.FromBytes(original).String() || !bytes.Equal(prepared.Documents()[reference].Data, original) {
				t.Fatal("dependency was not pinned to original blob bytes")
			}
			data, err := json.Marshal(prepared.Metadata()["checkout"])
			if err != nil || strings.Contains(string(data), "lookup") {
				t.Fatalf("dependency schema duplicated in consumer: %s %v", data, err)
			}
		})
	}
}

func TestLocalDependencyDocumentsAreComplete(t *testing.T) {
	checkout, extract, _ := checkoutConsumer(t, TargetPy)
	orders := Consumer{Name: "orders", Dir: checkout.Sources[0].Path, Server: &Source{Path: checkout.Sources[0].Path}, Targets: []string{TargetPy}, Sources: []Source{{Alias: "inventory", Contract: ordersMetadata()}}}
	prepared, err := Prepare(t.Context(), []Consumer{checkout, orders}, Options{Extract: extract})
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
	if p, err := Prepare(t.Context(), []Consumer{checkout, orders}, Options{Extract: extract}); err == nil || !strings.Contains(err.Error(), "contract dependency cycle") {
		if p != nil {
			p.Close()
		}
		t.Fatalf("cyclic document graph: %v", err)
	}
}
