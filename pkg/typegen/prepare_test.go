package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcpregistry"
)

func checkoutConsumer(t *testing.T, targets ...string) (Consumer, func(context.Context, string, string, []string, io.Writer) error) {
	t.Helper()
	root := t.TempDir()
	checkout, orders := filepath.Join(root, "checkout"), filepath.Join(root, "orders")
	writeProvider(t, orders, "orders", pythonProvider)
	put(t, checkout, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	put(t, checkout, "server.ts", "export default {};\n")
	put(t, checkout, "server.json", string(mustJSON(t, map[string]any{"name": "example.com/checkout", "version": "1.0.0", "description": "Checkout"})))
	own := mcpcontract.Tool{Name: "status", Input: json.RawMessage(`{"type":"object","additionalProperties":false}`)}
	results := map[string]any{
		"example.com/checkout": extraction{Tools: []mcpcontract.Tool{own}, Sources: map[string]string{"server.ts": "sha256:checkout"}},
		"example.com/orders":   extraction{Tools: []mcpcontract.Tool{lookupTool}},
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
		for dir, target := range map[string]string{"holon-sh": TargetTS, "holon-ui": TargetUI, "python": TargetPy} {
			if err := os.MkdirAll(filepath.Join(stage, dir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := prepared.WriteSlot("checkout", target, filepath.Join(stage, dir)); err != nil {
				t.Fatal(err)
			}
		}
		// The own server's tool is status; its dependency's is lookup.
		aliased := test.alias != ""
		node, python := read(t, stage, "holon-sh/generated/index.d.ts"), read(t, stage, "python/holon/apps/__init__.py")
		if !strings.Contains(node, `"lookup"`) || strings.Contains(node, `"`+test.key+`": {`) != aliased || strings.Contains(python, "def status(") != aliased {
			t.Fatalf("server clients under alias %q:\n%s\n%s", test.alias, node, python)
		}
		if browser := read(t, stage, "holon-ui/generated/index.d.ts"); !strings.Contains(browser, `"lookup"`) || !strings.Contains(browser, `type OwnAppKey = "`+test.key+`"`) || strings.Contains(browser, `"checkout": {`) == aliased {
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

func TestServersDescribeEachConsumersOwnServer(t *testing.T) {
	info := json.RawMessage(`{"name":"example.com/chat","version":"2.0.0","title":"Chat","description":"Chat with Holon","websiteUrl":"https://example.com/chat"}`)
	for _, test := range []struct {
		name, descriptor string
		want             *mcpregistry.Descriptor
	}{
		{"MCP constructor", "", &mcpregistry.Descriptor{Name: "example.com/chat", Version: "2.0.0", Title: "Chat", Description: "Chat with Holon", WebsiteURL: "https://example.com/chat"}},
		{"server.json", `{"name":"example.com/chat","version":"1.0.0","description":"Chat"}`, &mcpregistry.Descriptor{Name: "example.com/chat", Version: "1.0.0", Description: "Chat"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			chat := t.TempDir()
			put(t, chat, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
			put(t, chat, "server.ts", "export default {};\n")
			if test.descriptor != "" {
				put(t, chat, "server.json", test.descriptor)
			}
			options := Options{Run: fakeRun(map[string]any{"": extraction{ServerInfo: info}, "example.com/chat": extraction{}})}
			prepared, err := Prepare(t.Context(), []Consumer{{Name: "chat", Dir: chat, Server: &Source{Path: filepath.Join(chat, "server.ts")}}}, options)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			if got := prepared.Servers()["chat"]; !reflect.DeepEqual(got, test.want) {
				t.Fatalf("chat server = %+v, want %+v", got, test.want)
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
		"example.com/orders": extraction{Tools: []mcpcontract.Tool{lookupTool}},
		"example.com/broken": &RunnerError{Status: 2, Stderr: "tools are registered dynamically"},
		"example.com/---":    extraction{Tools: []mcpcontract.Tool{lookupTool}},
		"":                   extraction{ServerInfo: json.RawMessage(`{"name":"provider"}`)},
	}
	source := func(name string) Source { return Source{Path: filepath.Join(root, name)} }
	orders := source("orders")
	failure := errors.New("buildx unavailable")
	failing := func(context.Context, string, string, []string, io.Writer) error { return failure }
	for _, test := range []struct {
		name    string
		server  *Source
		sources []Source
		run     func(context.Context, string, string, []string, io.Writer) error
		want    string
	}{
		{name: "unnamed server", sources: []Source{source("nameless")}, want: "declare the server's name, version and description in its MCP constructor, or add a server.json"},
		{name: "go without server.json", sources: []Source{source("go")}, want: "needs a server.json"},
		{name: "extraction diagnostic", sources: []Source{source("broken")}, want: "tools are registered dynamically"},
		{name: "extractor failure", sources: []Source{orders}, run: failing, want: failure.Error()},
		{name: "invalid derived alias", sources: []Source{source("odd")}, want: `cannot derive a client name from "example.com/---"; write ALIAS=`},
		{name: "duplicate alias", sources: []Source{orders, source("orders-copy")}, want: "both become apps.orders"},
		{name: "explicit aliases", sources: []Source{orders, {Alias: "backup", Path: filepath.Join(root, "orders-copy")}, {Alias: "odd", Path: filepath.Join(root, "odd")}}},
		{name: "own server outside the consumer", server: &orders, want: "is outside"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := Options{Run: test.run}
			if options.Run == nil {
				options.Run = fakeRun(results)
			}
			prepared, err := Prepare(t.Context(), []Consumer{{Name: "app", Dir: app, Targets: []string{TargetTS}, Server: test.server, Sources: test.sources}}, options)
			if prepared != nil {
				defer prepared.Close()
			}
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Prepare = %v, want %q", err, test.want)
			}
		})
	}
}

func TestGenerateInstallsADeclaredRuntime(t *testing.T) {
	c, extract := checkoutConsumer(t, TargetTS)
	bin := t.TempDir()
	put(t, bin, "npm", "#!/bin/sh\nset -e\n[ \"$INSTALL_MODE\" = from-options ]\nprintf '{}' > package-lock.json\nmkdir -p node_modules/holon-sh/generated\nprintf '%s' '{\"holon\":{\"slot\":\"generated\",\"contractVersion\":1}}' > node_modules/holon-sh/package.json\n")
	if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INSTALL_MODE", "inherited")
	var output bytes.Buffer
	if err := Generate(t.Context(), []Consumer{c}, Options{Stdout: &output, Stderr: &output, Run: extract, Env: []string{"INSTALL_MODE=from-options"}}); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	if client := read(t, c.Dir, "node_modules/holon-sh/generated/index.js"); !strings.Contains(client, `"lookup"`) {
		t.Fatalf("installed runtime has no generated client:\n%s", client)
	}
	put(t, bin, "npm", "#!/bin/sh\nexit 17\n")
	if err := Generate(t.Context(), []Consumer{c}, Options{Run: extract}); err != nil {
		t.Fatalf("installed runtime was reinstalled: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(c.Dir, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := Generate(t.Context(), []Consumer{c}, Options{Run: extract}); err == nil || !strings.Contains(err.Error(), "exit status 17") {
		t.Fatalf("failed installation was not reported: %v", err)
	}
	put(t, c.Dir, "package.json", `{"type":"module"}`)
	err := Generate(t.Context(), []Consumer{c}, Options{Run: extract})
	if err == nil || !strings.Contains(err.Error(), "does not depend on holon-sh; run npm install holon-sh") {
		t.Fatalf("undeclared runtime = %v", err)
	}
	py := Consumer{Name: "py", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: c.Sources}
	err = Generate(t.Context(), []Consumer{py}, Options{Run: extract})
	if err == nil || !strings.Contains(err.Error(), "does not depend on holon-sh; run uv add holon-sh") {
		t.Fatalf("undeclared python runtime = %v", err)
	}
}

func TestGenerateGivesEachProjectItsOwnNodeSlot(t *testing.T) {
	root := t.TempDir()
	// first finds the hoisted package above it; second links to the same package, as pnpm does.
	pkg := slotPackage(t, root, "holon-sh")
	put(t, pkg, "generated/index.js", "placeholder")
	writeProvider(t, filepath.Join(root, "orders"), "orders", pythonProvider)
	var consumers []Consumer
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(root, name)
		put(t, dir, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
		consumers = append(consumers, Consumer{Name: name, Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Path: filepath.Join(root, "orders")}}})
	}
	if err := os.MkdirAll(filepath.Join(root, "second/node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pkg, filepath.Join(root, "second/node_modules/holon-sh")); err != nil {
		t.Fatal(err)
	}
	options := Options{Run: fakeRun(map[string]any{"example.com/orders": extraction{Tools: []mcpcontract.Tool{lookupTool}}})}
	if err := Generate(t.Context(), consumers, options); err == nil || !strings.Contains(err.Error(), "install a separate") {
		t.Fatalf("shared slot not diagnosed: %v", err)
	}
	if got := read(t, pkg, "generated/index.js"); got != "placeholder" {
		t.Fatal("shared slot changed before rejection")
	}
	if err := Generate(t.Context(), consumers[:1], options); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, pkg, "generated/index.js"), `"lookup"`) {
		t.Fatal("first project's clients were not generated")
	}
	if err := Generate(t.Context(), consumers[1:], options); err == nil || !strings.Contains(err.Error(), "install a separate") {
		t.Fatalf("a later project took over the slot: %v", err)
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
	if err := prepared.WriteSlot("caller", TargetPy, c.Dir); err != nil {
		t.Fatal(err)
	}
	if module := read(t, c.Dir, "holon/apps/__init__.py"); !strings.Contains(module, "async def lookup(") || !strings.Contains(module, "async def status(") {
		t.Fatal("pre-resolved contract did not generate a typed client")
	}
}

func TestGoConsumerClients(t *testing.T) {
	root := t.TempDir()
	put(t, root, "go.mod", "module example.com/caller\n\ngo 1.27.0\n")
	put(t, root, "server.go", "package main\n\nfunc main() {}\n")
	put(t, root, "server.json", `{"name":"example.com/caller","version":"1.0.0","description":"Caller"}`)
	c := Consumer{Name: "caller", Dir: root, Targets: []string{TargetGo}, Server: &Source{Path: root}, Sources: []Source{{Document: document(t, ordersMetadata())}}}
	if err := Generate(t.Context(), []Consumer{c}, Options{}); err != nil {
		t.Fatal(err)
	}
	clients := read(t, root, "holon/clients.go")
	if !strings.Contains(clients, `holon "github.com/holon-sh/sdk-go"`) || !strings.Contains(clients, " Lookup(") {
		t.Fatalf("Go client lacks the default runtime or dependency tool:\n%s", clients)
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
	// Check verifies the application files the server loaded, not the slot an image build replaces.
	c := Consumer{Name: "orders", Dir: orders, Server: &Source{Path: orders}, Targets: []string{TargetPy}, Sources: []Source{{Document: document(t, ordersMetadata())}}}
	if err := os.MkdirAll(filepath.Join(orders, "holon/apps/__pycache__"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Generate(t.Context(), []Consumer{c}, Options{}); err != nil {
		t.Fatalf("generate beside a holon/apps directory without a module: %v", err)
	}
	prepared, err := Prepare(t.Context(), []Consumer{c}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	check, _ := prepared.CheckConfig("orders")
	if sources := check["sources"].(map[string]string); sources["server.py"] == "" || sources["holon/apps/__init__.py"] != "" || !strings.Contains(read(t, orders, "holon/apps/__init__.py"), "async def lookup(") {
		t.Fatalf("check sources = %v", sources)
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
	slotPackage(t, app, nodePackages[TargetTS])
	c := Consumer{Name: "app", Dir: app, Targets: []string{TargetTS}, Sources: []Source{{Path: orders}}}
	options := Options{Run: fakeRun(map[string]any{"example.com/orders": extraction{Tools: ordersMetadata().Schema.Tools}})}
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

// containerRun runs the real runner as an image build does: the server's own directory is not
// visible there, so it must be given a snapshot of it.
func containerRun(origin string) func(context.Context, string, string, []string, io.Writer) error {
	return func(ctx context.Context, language, tooling string, args []string, stdout io.Writer) error {
		if pathInsideContext(args[1], origin) {
			return fmt.Errorf("%s is not a snapshot of %s", args[1], origin)
		}
		command := exec.CommandContext(ctx, "node", append([]string{filepath.Join(tooling, language, "runner.mjs")}, args...)...)
		command.Stdout = stdout
		return command.Run()
	}
}

func TestAnExtractionHookRunsSnapshotsThatNeedNothingInstalled(t *testing.T) {
	needNode(t)
	orders := t.TempDir()
	put(t, orders, "server.json", `{"name":"example.com/orders","version":"1.0.0","description":"Orders"}`)
	put(t, orders, "server.ts", `import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { z } from "zod";
export default createMcpHandler(() => {
  const server = new McpServer({ name: "orders", version: "1" });
  server.registerTool("read", { inputSchema: z.object({ id: z.string() }) }, () => ({ content: [] }));
  return server;
});`)
	contract, err := ResolveDocument(t.Context(), Source{Path: orders}, Options{Run: containerRun(orders)})
	if err != nil || len(contract.Schema.Tools) != 1 || contract.Schema.Tools[0].Name != "read" {
		t.Fatalf("schema-only server in a snapshot = %+v, %v", contract, err)
	}
}
