package typegen

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/protocol"
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
	data := mustJSON(t, metadata)
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

// runtimePackage installs a minimal runtime package that runs contract-version-2 clients.
func runtimePackage(t *testing.T, app, pkg string) string {
	t.Helper()
	dir := filepath.Join(app, "node_modules", pkg)
	put(t, dir, "package.json", `{"name":"`+pkg+`","version":"0.0.1","exports":{"./runtime":{"types":"./runtime.d.ts"}},"holon":{"contractVersion":2}}`)
	return dir
}

// linkTypeScript makes the tooling's compiler available to app, whose node_modules must exist.
func linkTypeScript(t *testing.T, app string) {
	t.Helper()
	compiler, err := filepath.Abs("../../tooling/typescript/node_modules/typescript")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(compiler, filepath.Join(app, "node_modules/typescript")); err != nil {
		t.Fatal(err)
	}
}

// typeScriptApp makes a project with a stand-in holon-sh and the tooling's compiler.
func typeScriptApp(t *testing.T) string {
	t.Helper()
	needNode(t)
	app := t.TempDir()
	put(t, app, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	put(t, app, "tsconfig.json", `{"compilerOptions":{"strict":true,"noEmit":true,"skipLibCheck":true,"allowJs":true,"target":"ES2023","module":"NodeNext","moduleResolution":"NodeNext","allowImportingTsExtensions":true}}`)
	put(t, runtimePackage(t, app, "holon-sh"), "runtime.d.ts", "export type ToolClient<I, O, R> = unknown;\nexport declare class ToolCallError {}\nexport type EndpointBinding = unknown;\nexport type CreateOptions = unknown;\n")
	linkTypeScript(t, app)
	return app
}

func needPython(t *testing.T) string {
	t.Helper()
	program, err := typeLanguages["python"].localProgram(context.Background())
	if err != nil {
		t.Skip(err)
	}
	return program
}
func needNode(t *testing.T) {
	t.Helper()
	if _, err := typeLanguages["typescript"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
}

// fakeRun stands in for the language runners: it answers each snapshot from results, keyed by the
// snapshot's server.json name, serving an extraction or returning an error as the runner's failure.
func fakeRun(results map[string]any) func(context.Context, string, string, []string, io.Reader, io.Writer) error {
	return func(_ context.Context, _, tooling string, args []string, stdin io.Reader, stdout io.Writer) error {
		root, entry := args[1], args[2]
		if !lexicallyInside(root, filepath.Dir(tooling)) || !isFile(filepath.Join(root, entry)) {
			return fmt.Errorf("entry %s is not in a snapshot under %s: %s", entry, filepath.Dir(tooling), root)
		}
		var descriptor struct{ Name string }
		if err := readTypeJSON(filepath.Join(root, "server.json"), &descriptor); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		switch result := results[descriptor.Name].(type) {
		case extraction:
			return serveCatalog(stdin, stdout, result)
		case error:
			return result
		default:
			return fmt.Errorf("no fake result for %s", descriptor.Name)
		}
	}
}

func serveCatalog(stdin io.Reader, stdout io.Writer, result extraction) error {
	contract := cmp.Or(result.Contract, &mcpcontract.Contract{})
	info := result.ServerInfo
	if info == nil {
		info = json.RawMessage(`{"name":"example.com/fake","version":"1.0.0"}`)
	}
	encoder := json.NewEncoder(stdout)
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage
			Method string
			Params struct{ Cursor string }
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return err
		}
		if request.ID == nil {
			continue
		}
		page, _ := strconv.Atoi(request.Params.Cursor)
		answer := map[string]any{}
		switch request.Method {
		case "server/discover":
			answer["supportedVersions"] = []string{"2026-07-28"}
			answer["capabilities"] = map[string]any{"tools": map[string]any{}, "resources": map[string]any{}}
			answer["_meta"] = map[string]any{"io.modelcontextprotocol/serverInfo": info}
		case "tools/list":
			answer["tools"] = []mcpcontract.Tool{}
			if page < len(contract.Tools) {
				answer["tools"] = contract.Tools[page : page+1]
			}
			if page+1 < len(contract.Tools) {
				answer["nextCursor"] = strconv.Itoa(page + 1)
			}
		case "resources/list":
			listed := []any{}
			if page < len(contract.Resources) {
				resource := contract.Resources[page]
				listed = append(listed, map[string]any{"uri": resource.URI, "_meta": map[string]any{"sh.holon/outputSchema": resource.Output}})
			}
			answer["resources"] = listed
			if page+1 < len(contract.Resources) {
				answer["nextCursor"] = strconv.Itoa(page + 1)
			}
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": answer}); err != nil {
			return err
		}
	}
	return scanner.Err()
}

type registryResponse func(*http.Request) (*http.Response, error)

func (response registryResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	return response(request)
}

// imageClient serves an index annotated with its document blob; nil metadata leaves the index without a contract.
func imageClient(t *testing.T, metadata *mcpcontract.Metadata) *http.Client {
	t.Helper()
	type document struct {
		descriptor ocispec.Descriptor
		data       []byte
	}
	documents := map[string]document{}
	add := func(mediaType string, value any) ocispec.Descriptor {
		data := append([]byte(" "), mustJSON(t, value)...) // Fetched bytes need not match our writer.
		descriptor := content.NewDescriptorFromBytes(mediaType, data)
		documents[descriptor.Digest.String()] = document{descriptor, data}
		return descriptor
	}
	index := ocispec.Index{Manifests: []ocispec.Descriptor{}}
	if metadata != nil {
		layer := add(mcpcontract.MediaType, metadata)
		index.Annotations = map[string]string{mcpcontract.DigestAnnotation: layer.Digest.String()}
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
	inner := imageClient(t, metadata).Transport
	return &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
		if user, password, ok := request.BasicAuth(); !ok || user != "user" || password != "secret" {
			header := http.Header{"Www-Authenticate": {`Basic realm="images.example"`}, "Content-Type": {"application/json"}}
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: header, Request: request,
				Body: io.NopCloser(strings.NewReader(`{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`))}, nil
		}
		return inner.RoundTrip(request)
	})}
}

// registryImageSource resolves the test OCI package through the public registry source API.
func registryImageSource(t *testing.T, image *http.Client) (Source, *http.Client) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server":{"name":"example.com/orders","version":"1.0.0","description":"Orders","packages":[{"registryType":"oci","identifier":"images.example/orders:1.0.0"}]}}`))
	}))
	t.Cleanup(server.Close)
	source := Source{Registry: &protocol.RegistryRef{Name: "example.com/orders", Version: "1.0.0", Registry: server.URL}}
	client := &http.Client{Transport: hostTransport{
		strings.TrimPrefix(server.URL, "https://"): server.Client().Transport,
		"images.example": image.Transport,
	}}
	return source, client
}
