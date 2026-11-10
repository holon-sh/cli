package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func checkoutConsumer(t *testing.T, targets ...string) (Consumer, func(context.Context, string, string, []string, io.Reader, io.Writer) error) {
	t.Helper()
	root := t.TempDir()
	checkout, orders := filepath.Join(root, "checkout"), filepath.Join(root, "orders")
	writeProvider(t, orders, "orders", pythonProvider)
	put(t, checkout, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	put(t, checkout, "server.ts", "export default {};\n")
	put(t, checkout, "server.json", string(mustJSON(t, map[string]any{"name": "example.com/checkout", "version": "1.0.0", "description": "Checkout"})))
	own := mcpcontract.Tool{Name: "status", Input: json.RawMessage(`{"type":"object","additionalProperties":false}`)}
	results := map[string]any{
		"example.com/checkout": extraction{Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{own}}},
		"example.com/orders":   extraction{Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{lookupTool}}},
	}
	c := Consumer{Name: "checkout", Dir: checkout, Targets: targets, Server: &Source{Path: filepath.Join(checkout, "server.ts")}, Sources: []Source{{Path: orders}}}
	return c, fakeRun(results)
}

// Server code calls the project's own server only under an alias, and browser code always calls
// it. Either way it stays the project's own server, not a dependency of itself.
func TestTheOwnServerIsAClientOfBrowserCodeAndUnderAnAliasOfEverySlot(t *testing.T) {
	c, extract := checkoutConsumer(t, TargetTS, TargetUI, TargetPy)
	for _, test := range []struct{ alias, key string }{{"", "checkout"}, {"shop", "shop"}} {
		c.Server.Alias = test.alias
		prepared, err := Prepare(t.Context(), []Consumer{c}, Options{Run: extract})
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		stage := t.TempDir()
		if err := prepared.WriteClients("checkout", stage); err != nil {
			t.Fatal(err)
		}
		// The own server's tool is status; its dependency's is lookup.
		aliased := test.alias != ""
		node, python := read(t, stage, "generated/holon-sh/index.d.mts"), read(t, stage, "holon/apps/__init__.py")
		if !strings.Contains(node, `"lookup"`) || strings.Contains(node, `"`+test.key+`": {`) != aliased || strings.Contains(python, "def status(") != aliased {
			t.Fatalf("server clients under alias %q:\n%s\n%s", test.alias, node, python)
		}
		if browser := read(t, stage, "generated/holon-ui/index.d.mts"); !strings.Contains(browser, `"lookup"`) || !strings.Contains(browser, `type OwnAppKey = "`+test.key+`"`) || strings.Contains(browser, `"checkout": {`) == aliased {
			t.Fatalf("browser client under alias %q:\n%s", test.alias, browser)
		}
		if requires := prepared.Metadata()["checkout"].Requires; len(requires) != 1 {
			t.Fatalf("requires = %v", requires)
		}
	}
	c.Sources[0].Alias = "shop"
	if _, err := Prepare(t.Context(), []Consumer{c}, Options{Run: extract}); err == nil || !strings.Contains(err.Error(), "own client shop conflicts") {
		t.Fatalf("alias conflict = %v", err)
	}
}

// A consumer without a server.json or a server of its own announces the identity its project
// manifest gives it, read for the first of its targets whose manifest names the project.
func TestConsumersAnnounceTheirManifestIdentity(t *testing.T) {
	const node = `{"name":"@example/checkout","version":"1.2.3","dependencies":{"holon-sh":"*"}}`
	const python = "[build-system]\nrequires = ['hatchling']\n\n[project]\nname = 'checkout'\nversion = \"2.0\" # released\ndependencies = [\n  \"holon-sh\",\n]\n\n[project.urls]\nname = \"https://example.com\"\n"
	for _, test := range []struct {
		name    string
		files   map[string]string
		targets []string
		want    mcpcontract.Identity
	}{
		{"package.json", map[string]string{"package.json": node}, []string{TargetTS}, mcpcontract.Identity{Name: "@example/checkout", Version: "1.2.3"}},
		{"pyproject.toml", map[string]string{"pyproject.toml": python}, []string{TargetPy}, mcpcontract.Identity{Name: "checkout", Version: "2.0"}},
		{"go.mod", map[string]string{"go.mod": "module example.com/checkout\n\ngo 1.27\n\nrequire github.com/holon-sh/sdk-go v0.0.1\n"}, []string{TargetGo}, mcpcontract.Identity{Name: "example.com/checkout", Version: "0.0.0"}},
		{"only manifests of declared slots", map[string]string{"package.json": `{"name":"frontend"}`, "pyproject.toml": python}, []string{TargetPy}, mcpcontract.Identity{Name: "checkout", Version: "2.0"}},
		{"server.json first", map[string]string{"package.json": node, "server.json": `{"name":"example.com/checkout","version":"3.0.0","description":"Checkout"}`}, []string{TargetTS}, mcpcontract.Identity{Name: "example.com/checkout", Version: "3.0.0"}},
		{"an unnamed project", map[string]string{"package.json": `{"private":true}`}, []string{TargetTS}, mcpcontract.Identity{Name: "app", Version: "0.0.0"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range test.files {
				put(t, dir, name, content)
			}
			c := Consumer{Name: "app", Dir: dir, Targets: test.targets, Sources: []Source{{Document: document(t, ordersMetadata())}}}
			prepared, err := Prepare(t.Context(), []Consumer{c}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			if got := prepared.Metadata()["app"].Identity; got != test.want {
				t.Fatalf("identity = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestPrepareRejectsWhatItCannotResolve(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	put(t, app, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	writeProvider(t, filepath.Join(root, "orders"), "orders", pythonProvider)
	writeProvider(t, filepath.Join(root, "orders-copy"), "orders", pythonProvider)
	writeProvider(t, filepath.Join(root, "broken"), "broken", pythonProvider)
	writeProvider(t, filepath.Join(root, "odd"), "odd", pythonProvider)
	put(t, filepath.Join(root, "odd"), "server.json", `{"name":"example.com/---","version":"1.0.0","description":"Odd"}`)
	put(t, filepath.Join(root, "nameless"), "server.py", pythonProvider)
	put(t, filepath.Join(root, "go"), "server.go", "package main\n")
	results := map[string]any{
		"example.com/orders": extraction{Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{lookupTool}}},
		"example.com/broken": &RunnerError{Status: 2, Stderr: "tools are registered dynamically"},
		"example.com/---":    extraction{Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{lookupTool}}},
		"":                   extraction{ServerInfo: json.RawMessage(`{"name":"provider"}`)},
	}
	source := func(name string) Source { return Source{Path: filepath.Join(root, name)} }
	orders := source("orders")
	for _, test := range []struct {
		name    string
		server  *Source
		sources []Source
		want    string
	}{
		{name: "unnamed server", sources: []Source{source("nameless")}, want: "declare the server's name, version and description in its MCP constructor, or add a server.json"},
		{name: "go without server.json", sources: []Source{source("go")}, want: "needs a server.json"},
		{name: "extraction diagnostic", sources: []Source{source("broken")}, want: "tools are registered dynamically"},
		{name: "invalid derived alias", sources: []Source{source("odd")}, want: `cannot derive a client name from "example.com/---"; write ALIAS=`},
		{name: "duplicate alias", sources: []Source{orders, source("orders-copy")}, want: "both become apps.orders"},
		{name: "own server outside the consumer", server: &orders, want: "is outside"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Prepare(t.Context(), []Consumer{{Name: "app", Dir: app, Targets: []string{TargetTS}, Server: test.server, Sources: test.sources}}, Options{Run: fakeRun(results)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Prepare = %v, want %q", err, test.want)
			}
		})
	}
}

func TestGenerateInstallsADeclaredRuntime(t *testing.T) {
	t.Setenv("VIRTUAL_ENV", "")
	c, extract := checkoutConsumer(t, TargetTS)
	python := t.TempDir()
	put(t, python, "pyproject.toml", "[project]\nname = \"app\"\nversion = \"1\"\ndependencies = [\"holon-sh\"]\n")
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, test := range []struct {
		manager, install, installed string
		consumer                    Consumer
	}{
		{"npm", "mkdir -p node_modules/holon-sh\nprintf '%s' '{\"holon\":{\"contractVersion\":2}}' > node_modules/holon-sh/package.json", "node_modules/holon-sh/package.json", c},
		{"uv", "mkdir -p .venv/bin\n: > .venv/bin/python", ".venv/bin/python", Consumer{Name: "app", Dir: python, Targets: []string{TargetPy}}},
	} {
		put(t, bin, test.manager, "#!/bin/sh\nset -e\n"+test.install+"\n")
		if err := os.Chmod(filepath.Join(bin, test.manager), 0o755); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := Generate(t.Context(), []Consumer{test.consumer}, Options{Stdout: &output, Stderr: &output, Run: extract}); err != nil || !isFile(filepath.Join(test.consumer.Dir, test.installed)) {
			t.Fatalf("%s: generate: %v\n%s", test.manager, err, &output)
		}
		put(t, bin, test.manager, "#!/bin/sh\nexit 17\n")
		if err := Generate(t.Context(), []Consumer{test.consumer}, Options{Run: extract}); err != nil {
			t.Fatalf("%s: installed runtime was reinstalled: %v", test.manager, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(c.Dir, "node_modules")); err != nil {
		t.Fatal(err)
	}
	put(t, c.Dir, "package.json", `{"type":"module"}`)
	err := Generate(t.Context(), []Consumer{c}, Options{Run: extract})
	if err == nil || !strings.Contains(err.Error(), "does not depend on holon-sh; run npm install holon-sh") {
		t.Fatalf("undeclared runtime = %v", err)
	}
}

// A runtime of another contract gets one install, which can bring it up to date, before generation stops.
func TestGenerateRefusesARuntimeOfAnotherContract(t *testing.T) {
	root := t.TempDir()
	put(t, root, "node_modules/holon-sh/package.json", `{"name":"holon-sh","version":"0.0.1-dev.6","holon":{"slot":"generated","contractVersion":1}}`)
	app := filepath.Join(root, "app")
	put(t, app, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	bin := t.TempDir()
	put(t, bin, "npm", "#!/bin/sh\ntouch installed\n")
	if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := Consumer{Name: "app", Dir: app, Targets: []string{TargetTS}, Sources: []Source{{Document: document(t, ordersMetadata())}}}
	if err := Generate(t.Context(), []Consumer{c}, Options{}); err == nil || !strings.Contains(err.Error(), "holon-sh 0.0.1-dev.6 keeps generated clients inside node_modules; upgrade it") || !isFile(filepath.Join(app, "installed")) {
		t.Fatalf("contract 1 runtime = %v", err)
	}
}

// Holon writes clients only through real directories inside the project.
func TestGenerateRefusesASymlinkOutOfTheProject(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "generated")); err != nil {
		t.Fatal(err)
	}
	if err := writeSlot(dir, TargetTS, map[string][]byte{"index.mjs": []byte("// " + mcpcontract.Marker + " app=x\n")}); err == nil {
		t.Fatal("wrote through a symlinked directory")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("wrote outside the project")
	}
}

func TestSuppliedContractsRemainDistinct(t *testing.T) {
	metadata := ordersMetadata()
	source := Source{Document: document(t, metadata)}
	alternate := ordersMetadata()
	alternate.Schema = &mcpcontract.Contract{Tools: []mcpcontract.Tool{{Name: "status", Input: json.RawMessage(`{"type":"object"}`)}}}
	c := Consumer{Name: "caller", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: []Source{source, {Alias: "alternate", Document: document(t, alternate)}}}
	prepared, err := Prepare(t.Context(), []Consumer{c}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if err := prepared.WriteClients("caller", c.Dir); err != nil {
		t.Fatal(err)
	}
	if module := read(t, c.Dir, "holon/apps/__init__.py"); !strings.Contains(module, "async def lookup(") || !strings.Contains(module, "async def status(") {
		t.Fatal("pre-resolved contract did not generate a typed client")
	}
	// Python's bytecode cache beside the clients does not make them someone else's.
	put(t, c.Dir, "holon/apps/__pycache__/__init__.cpython-313.pyc", "bytecode")
	if err := prepared.WriteClients("caller", c.Dir); err != nil {
		t.Fatal(err)
	}
}

func TestAServerThatImportsItsOwnClientsExtractsBeforeTheyExist(t *testing.T) {
	needPython(t)
	orders := t.TempDir()
	writeProvider(t, orders, "orders", "from holon.apps import apps\n"+pythonProvider)
	put(t, orders, "requirements.txt", "holon-sh\n")
	// Stands in for the installed holon-sh package that generated clients import.
	put(t, orders, "holon/client.py", "class Apps:\n    def __init__(self, *args):\n        pass\ndef __getattr__(name):\n    return object\n")
	contract, err := ResolveDocument(t.Context(), Source{Path: orders}, Options{})
	if err != nil || len(contract.Schema.Tools) != 1 || contract.Schema.Tools[0].Name != "read" {
		t.Fatalf("ResolveDocument = %+v, %v", contract, err)
	}
	if _, err := os.Stat(filepath.Join(orders, "holon/apps")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("extraction wrote the generated slot: %v", err)
	}
}

// committedContract writes metadata as holon types generate --contract prints it.
func committedContract(t *testing.T, dir string, metadata *mcpcontract.Metadata) {
	t.Helper()
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	put(t, dir, ContractFile, string(data)+"\n")
}

func TestACommittedContractStandsInForMissingDependencies(t *testing.T) {
	needNode(t)
	orders := t.TempDir()
	put(t, orders, "server.ts", `import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import Database from "better-sqlite3";
export const driver = Database;
export default createMcpHandler(() => new McpServer({ name: "orders", version: "1" }));`)
	put(t, orders, "server.json", `{"name":"example.com/orders","version":"1.0.0","description":"Orders"}`)
	if _, err := ResolveDocument(t.Context(), Source{Path: orders}, Options{}); err == nil || !strings.Contains(err.Error(), "better-sqlite3 is not installed") {
		t.Fatalf("without the package or a committed contract: %v", err)
	}
	committedContract(t, orders, ordersMetadata())
	contract, err := ResolveDocument(t.Context(), Source{Path: orders}, Options{})
	if err != nil || len(contract.Schema.Tools) != 1 || contract.Schema.Tools[0].Name != "lookup" {
		t.Fatalf("committed contract = %+v, %v", contract, err)
	}
	other := ordersMetadata()
	other.Identity.Version = "2.0.0"
	committedContract(t, orders, other)
	if _, err := ResolveDocument(t.Context(), Source{Path: orders}, Options{}); err == nil || !strings.Contains(err.Error(), "describes example.com/orders@2.0.0") {
		t.Fatalf("committed contract for another version: %v", err)
	}
}

func TestACommittedContractThatDiffersFromTheServerIsStale(t *testing.T) {
	root := t.TempDir()
	orders, app := filepath.Join(root, "orders"), filepath.Join(root, "app")
	writeProvider(t, orders, "orders", pythonProvider)
	put(t, app, "package.json", "{}")
	c := Consumer{Name: "app", Dir: app, Targets: []string{TargetTS}, Sources: []Source{{Path: orders}}}
	options := Options{Run: fakeRun(map[string]any{"example.com/orders": extraction{Contract: ordersMetadata().Schema}})}
	committedContract(t, orders, ordersMetadata())
	prepared, err := Prepare(t.Context(), []Consumer{c}, options)
	if err != nil {
		t.Fatalf("matching committed contract: %v", err)
	}
	_ = prepared.Close()
	stale := ordersMetadata()
	stale.Schema.Tools[0].Name = "find"
	committedContract(t, orders, stale)
	if _, err := Prepare(t.Context(), []Consumer{c}, options); err == nil || !strings.Contains(err.Error(), filepath.Join(orders, ContractFile)+" is stale") {
		t.Fatalf("stale committed contract: %v", err)
	}
	// Printing the contract regenerates the file, so it answers what the server does.
	if contract, err := ResolveDocument(t.Context(), Source{Path: orders}, options); err != nil || contract.Schema.Tools[0].Name != "lookup" {
		t.Fatalf("printed contract = %+v, %v", contract, err)
	}
}

// A runner's server answers what is on its stdin and exits. Its catalog is listed page by page, sorted,
// with each schema as the server wrote it, by a client that sends files.
func TestARunnersCatalogIsListedPageByPage(t *testing.T) {
	orders := t.TempDir()
	writeProvider(t, orders, "orders", pythonProvider)
	read := mcpcontract.Tool{Name: "read", Input: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","maximum":12345678901234567890}}}`)}
	resources := []mcpcontract.Resource{{URI: "orders://typed", Output: json.RawMessage(`{"type":"string"}`)}, {URI: "orders://all", Output: json.RawMessage(`{"type":"array"}`)}}
	serve := fakeRun(map[string]any{"example.com/orders": extraction{Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{read, lookupTool}, Resources: resources}}})
	run := func(ctx context.Context, language, tooling string, args []string, stdin io.Reader, stdout io.Writer) error {
		data, err := io.ReadAll(stdin)
		if err != nil || !bytes.Contains(data, []byte(`"io.modelcontextprotocol/files"`)) {
			t.Errorf("the client does not send files: %s", data)
		}
		return serve(ctx, language, tooling, args, bytes.NewReader(data), stdout)
	}
	document, err := ResolveDocument(t.Context(), Source{Path: orders}, Options{Run: run})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"identity":{"name":"example.com/orders","version":"1.0.0"},"schema":{"tools":[` + string(mustJSON(t, lookupTool)) + `,` + string(mustJSON(t, read)) +
		`],"resources":[{"uri":"orders://all","outputSchema":{"type":"array"}},{"uri":"orders://typed","outputSchema":{"type":"string"}}]}}`
	if string(document.Data) != want {
		t.Fatalf("contract = %s\nwant %s", document.Data, want)
	}
}

func TestAServerThatDoesNotAnswerIsStopped(t *testing.T) {
	needPython(t)
	limit := serverTimeout
	serverTimeout = time.Second
	t.Cleanup(func() { serverTimeout = limit })
	orders := t.TempDir()
	writeProvider(t, orders, "orders", "while True:\n    pass\n")
	if _, err := ResolveDocument(t.Context(), Source{Path: orders}, Options{}); err == nil || !strings.Contains(err.Error(), "server.py: no catalog within 1s") {
		t.Fatalf("server that never answers: %v", err)
	}
}
