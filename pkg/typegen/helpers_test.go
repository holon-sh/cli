package typegen

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

const pythonProvider = "from mcp.server import MCPServer\nserver = MCPServer(\"provider\")\n@server.tool()\ndef read(id: str) -> str:\n    return id\n"

var lookupTool = mcpcontract.Tool{Name: "lookup", Input: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
	Output: json.RawMessage(`{"type":"object","properties":{"total":{"type":"number"}},"required":["total"],"additionalProperties":false}`)}

func ordersMetadata() *mcpcontract.Metadata {
	return &mcpcontract.Metadata{Identity: mcpcontract.Identity{Name: "example.com/orders", Version: "1.0.0"},
		Schema: &mcpcontract.Contract{Tools: []mcpcontract.Tool{lookupTool}}}
}

// document supplies metadata as a source document: its bytes as marshaled, validated only when resolved.
func document(t *testing.T, metadata *mcpcontract.Metadata) *Document {
	t.Helper()
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	return &Document{Metadata: metadata, Data: data, Digest: digest.FromBytes(data).String()}
}

func put(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func read(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// writeProvider writes a Python MCP server named example.com/<name> into root.
func writeProvider(t *testing.T, root, name, source string) {
	t.Helper()
	put(t, root, "server.py", source)
	put(t, root, "server.json", string(mustJSON(t, map[string]any{"name": "example.com/" + name, "version": "1.0.0", "description": name})))
}

// slotPackage installs a minimal placeholder runtime package that declares a generation slot.
func slotPackage(t *testing.T, app, pkg string) string {
	t.Helper()
	dir := filepath.Join(app, "node_modules", pkg)
	put(t, dir, "package.json", `{"name":"`+pkg+`","holon":{"slot":"generated","contractVersion":1}}`)
	put(t, dir, "generated/holon-generated.json", string(mustJSON(t, slotMetadata{Marker: mcpcontract.Marker, Placeholder: true})))
	return dir
}

func needPython(t *testing.T) {
	t.Helper()
	if _, err := typeLanguages["python"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
}
func needNode(t *testing.T) {
	t.Helper()
	if _, err := typeLanguages["typescript"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
}

// fakeExtract stands in for the language runners: it answers each snapshot from results, keyed by
// the server.json name beside the snapshot's entry, and records the languages it was asked for.
func fakeExtract(t *testing.T, results map[string]extraction, calls *[]string) func(context.Context, string, string, string, string) error {
	t.Helper()
	return func(_ context.Context, language, tooling, config, output string) error {
		*calls = append(*calls, language)
		if !isFile(filepath.Join(tooling, language, typeLanguages[language].runner)) {
			return fmt.Errorf("tooling for %s is not unpacked under %s", language, tooling)
		}
		var request struct {
			Apps []struct{ Name, Path, Entry string }
		}
		if err := readTypeJSON(config, &request); err != nil {
			return err
		}
		found := map[string]extraction{}
		for _, app := range request.Apps {
			if !isFile(filepath.Join(app.Path, app.Entry)) {
				return fmt.Errorf("%s entry %s is not in its snapshot", app.Name, app.Entry)
			}
			var descriptor struct{ Name string }
			if err := readTypeJSON(filepath.Join(app.Path, "server.json"), &descriptor); err != nil {
				return err
			}
			result, ok := results[descriptor.Name]
			if !ok {
				return fmt.Errorf("no fake result for %s", descriptor.Name)
			}
			found[app.Name] = result
		}
		return writeJSON(output, found)
	}
}

type registryResponse func(*http.Request) (*http.Response, error)

func (response registryResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	return response(request)
}

// imageClient serves an annotated index and its document blob. An empty annotation
// uses the blob digest; nil metadata leaves the index without a contract.
func imageClient(t *testing.T, metadata *mcpcontract.Metadata, annotation string) *http.Client {
	t.Helper()
	type document struct {
		descriptor ocispec.Descriptor
		data       []byte
	}
	documents := map[string]document{}
	add := func(mediaType string, value any) ocispec.Descriptor {
		data := mustJSON(t, value)
		descriptor := content.NewDescriptorFromBytes(mediaType, data)
		documents[descriptor.Digest.String()] = document{descriptor, data}
		return descriptor
	}
	index := ocispec.Index{Manifests: []ocispec.Descriptor{}}
	if metadata != nil {
		layer := add(mcpcontract.MediaType, metadata)
		index.Annotations = map[string]string{mcpcontract.DigestAnnotation: cmp.Or(annotation, layer.Digest.String())}
	}
	index.SchemaVersion = 2
	descriptor := add(ocispec.MediaTypeImageIndex, index)
	documents["1.0.0"] = documents[descriptor.Digest.String()]
	return &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "images.example" || !strings.HasPrefix(request.URL.Path, "/v2/orders/") {
			t.Errorf("image dependency requested a different registry: %s", request.URL)
			return nil, fmt.Errorf("unexpected registry request: %s", request.URL)
		}
		doc, ok := documents[filepath.Base(request.URL.Path)]
		if !ok {
			return nil, fmt.Errorf("unexpected OCI document: %s", request.URL)
		}
		header := make(http.Header)
		header.Set("Content-Type", doc.descriptor.MediaType)
		header.Set("Content-Length", strconv.Itoa(len(doc.data)))
		header.Set("Docker-Content-Digest", doc.descriptor.Digest.String())
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(doc.data)), ContentLength: int64(len(doc.data)), Request: request}, nil
	})}
}

// authenticatedRegistry serves imageClient's documents only with HTTP basic credentials user:secret.
func authenticatedRegistry(t *testing.T, metadata *mcpcontract.Metadata) *http.Client {
	t.Helper()
	inner := imageClient(t, metadata, "").Transport
	return &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
		if user, password, ok := request.BasicAuth(); !ok || user != "user" || password != "secret" {
			header := http.Header{"Www-Authenticate": {`Basic realm="images.example"`}, "Content-Type": {"application/json"}}
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: header, Request: request,
				Body: io.NopCloser(strings.NewReader(`{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`))}, nil
		}
		return inner.RoundTrip(request)
	})}
}
