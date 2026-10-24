package mcptype

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// The C and npm adapters must pass the same wire requests to the compiler and
// return the same bytes, including binary runtime files and the new slot fields.
func TestNPMPackageParity(t *testing.T) {
	archive, err := os.Open(filepath.Join("vendor", "mcptype-0.0.6.tgz"))
	if err != nil {
		t.Fatalf("fetch the pinned npm compiler with scripts/fetch-mcptype: %v", err)
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	root := t.TempDir()
	packageDir := filepath.Join(root, "node_modules", "mcptype")
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name, ok := strings.CutPrefix(header.Name, "package/")
		if !ok || name == "" || header.Typeflag == tar.TypeDir {
			continue
		}
		if !filepath.IsLocal(name) || header.Typeflag != tar.TypeReg {
			t.Fatalf("unsupported compiler archive entry %q", header.Name)
		}
		file := filepath.Join(packageDir, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	identity := mcpcontract.Identity{Name: "example/checkout", Version: "1.0.0"}
	contract := &mcpcontract.Contract{
		Tools:     []mcpcontract.Tool{{Name: "get", Input: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"lookup"}},"required":["id"]}`), Output: json.RawMessage(`{"type":"object","properties":{"total":{"type":"number"}}}`)}},
		Resources: []mcpcontract.Resource{{URI: "orders://summary", Output: json.RawMessage(`{"type":"object"}`)}},
	}
	document := map[string]any{"metadata": mcpcontract.Metadata{Version: mcpcontract.Version, Identity: identity, Contract: contract}}
	var requests []GenerateRequest
	for _, target := range []string{"node", "browser", "python"} {
		request := GenerateRequest{
			Target: target, Identity: identity,
			Requires:  map[string]mcpcontract.Identity{"orders": {Name: "example/orders", Version: "2.0.0"}},
			Contracts: map[string]*mcpcontract.Contract{"orders": contract},
			Runtime:   map[string][]byte{"bytes.dat": {0, 128, 255}},
			Banner:    "holon-generated-apps-v1 app=checkout",
		}
		if target == "python" {
			request.PythonModule, request.PythonRuntimeModule = "holon/apps/__init__.py", "holon.client"
		} else {
			request.RuntimeSpecifier, request.BrowserRuntimeSpecifier, request.ProtocolSpecifier = "../runtime.mjs", "../browser-runtime.mjs", "../protocol.js"
		}
		requests = append(requests, request)
	}
	input, err := json.Marshal(map[string]any{"document": document, "requests": requests})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from 'node:fs';
import { canonical, generate } from 'mcptype/compiler';
const { document, requests } = JSON.parse(readFileSync(0, 'utf8'));
const files = [];
for (const request of requests) {
  request.runtime = Object.fromEntries(Object.entries(request.runtime).map(([name, bytes]) => [name, Buffer.from(bytes, 'base64')]));
  const generated = await generate(request);
  files.push(Object.fromEntries(Object.entries(generated.files).map(([name, bytes]) => [name, bytes.toString('base64')])));
}
console.log(JSON.stringify({ canonical: await canonical(document), files }));
`)
	command.Dir, command.Stdin = root, bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("npm compiler: %v\n%s", err, output)
	}
	var npm struct {
		Canonical CanonicalForm       `json:"canonical"`
		Files     []map[string][]byte `json:"files"`
	}
	if err := json.Unmarshal(output, &npm); err != nil {
		t.Fatal(err)
	}
	canonical, err := Canonical(t.Context(), document)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canonical, npm.Canonical) {
		t.Fatalf("C ABI canonical differs from npm:\nC: %+v\nnpm: %+v", canonical, npm.Canonical)
	}
	if len(npm.Files) != len(requests) {
		t.Fatalf("npm returned %d packages, expected %d", len(npm.Files), len(requests))
	}
	for index, request := range requests {
		files, err := Generate(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(files, npm.Files[index]) {
			t.Errorf("C ABI %s slot bytes differ from npm", request.Target)
		}
	}
}
