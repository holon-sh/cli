package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func checkoutConsumer(t *testing.T, targets ...string) (Consumer, func(context.Context, string, string, string, string) error) {
	t.Helper()
	root := t.TempDir()
	checkout, orders := filepath.Join(root, "checkout"), filepath.Join(root, "orders")
	writeProvider(t, orders, "orders", pythonProvider)
	put(t, checkout, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	put(t, checkout, "server.ts", "export default {};\n")
	put(t, checkout, "server.json", string(mustJSON(t, map[string]any{"name": "example.com/checkout", "version": "1.0.0", "description": "Checkout"})))
	own := mcpcontract.Tool{Name: "status", Input: json.RawMessage(`{"type":"object","additionalProperties":false}`)}
	results := map[string]extraction{
		"example.com/checkout": {Tools: []mcpcontract.Tool{own}, Sources: map[string]string{"server.ts": "sha256:checkout"}},
		"example.com/orders":   {Tools: []mcpcontract.Tool{lookupTool}},
	}
	c := Consumer{Name: "checkout", Dir: checkout, Targets: targets, Server: &Source{Path: filepath.Join(checkout, "server.ts")}, Sources: []Source{{Path: orders}}}
	return c, fakeExtract(results)
}

func TestPrepareGeneratesNodeSlotsFromExplicitSources(t *testing.T) {
	c, extract := checkoutConsumer(t, TargetTS)
	prepared, err := Prepare(t.Context(), []Consumer{c}, Options{Extract: extract})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	metadata := prepared.Metadata()["checkout"]
	if metadata.Identity.Name != "example.com/checkout" || metadata.Schema == nil || len(prepared.Documents()[metadata.Requires["orders"]].Schema.Tools) != 1 {
		t.Fatalf("consumer metadata = %+v", metadata)
	}
	stage := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stage, "holon-sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = prepared.WriteNodeSlot("checkout", filepath.Join(stage, "holon-sh")); err != nil {
		t.Fatal(err)
	}
	// Server code calls its dependencies; browser code also calls the server itself.
	if node := read(t, stage, "holon-sh/generated/index.js"); !strings.Contains(node, `"lookup"`) || strings.Contains(node, `"status"`) {
		t.Fatalf("node client:\n%s", node)
	}
	if browser := read(t, stage, "holon-sh/generated/browser.js"); !strings.Contains(browser, `"lookup"`) || !strings.Contains(browser, `"status"`) {
		t.Fatalf("browser client:\n%s", browser)
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
	results := map[string]extraction{
		"example.com/orders": {Tools: []mcpcontract.Tool{lookupTool}},
		"example.com/broken": {Diagnostic: "tools are registered dynamically"},
		"example.com/---":    {Tools: []mcpcontract.Tool{lookupTool}},
	}
	source := func(name string) Source { return Source{Path: filepath.Join(root, name)} }
	orders := source("orders")
	failure := errors.New("buildx unavailable")
	failing := func(context.Context, string, string, string, string) error { return failure }
	for _, test := range []struct {
		name    string
		server  *Source
		sources []Source
		extract func(context.Context, string, string, string, string) error
		want    string
	}{
		{name: "missing server.json", sources: []Source{source("nameless")}, want: "needs a server.json"},
		{name: "extraction diagnostic", sources: []Source{source("broken")}, want: "tools are registered dynamically"},
		{name: "extractor failure", sources: []Source{orders}, extract: failing, want: failure.Error()},
		{name: "invalid derived alias", sources: []Source{source("odd")}, want: `cannot derive a client name from "example.com/---"; write ALIAS=`},
		{name: "duplicate alias", sources: []Source{orders, source("orders-copy")}, want: "both become apps.orders"},
		{name: "explicit aliases", sources: []Source{orders, {Alias: "backup", Path: filepath.Join(root, "orders-copy")}, {Alias: "odd", Path: filepath.Join(root, "odd")}}},
		{name: "own server outside the consumer", server: &orders, want: "is outside"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := Options{Extract: test.extract}
			if options.Extract == nil {
				options.Extract = fakeExtract(results)
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
	put(t, bin, "npm", "#!/bin/sh\nset -e\n[ \"$HOLON_SKIP_POSTINSTALL\" = 1 ]\n[ \"$INSTALL_MODE\" = from-options ]\nprintf '{}' > package-lock.json\nmkdir -p node_modules/holon-sh/generated\nprintf '%s' '{\"holon\":{\"slot\":\"generated\",\"contractVersion\":1}}' > node_modules/holon-sh/package.json\n")
	if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INSTALL_MODE", "inherited")
	var output bytes.Buffer
	if err := Generate(t.Context(), []Consumer{c}, Options{Stdout: &output, Stderr: &output, Extract: extract, Env: []string{"INSTALL_MODE=from-options"}}); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	if client := read(t, c.Dir, "node_modules/holon-sh/generated/index.js"); !strings.Contains(client, `"lookup"`) {
		t.Fatalf("installed runtime has no generated client:\n%s", client)
	}
	put(t, bin, "npm", "#!/bin/sh\nexit 17\n")
	if err := Generate(t.Context(), []Consumer{c}, Options{Extract: extract}); err != nil {
		t.Fatalf("installed runtime was reinstalled: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(c.Dir, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := Generate(t.Context(), []Consumer{c}, Options{Extract: extract}); err == nil || !strings.Contains(err.Error(), "exit status 17") {
		t.Fatalf("failed installation was not reported: %v", err)
	}
	put(t, c.Dir, "package.json", `{"type":"module"}`)
	err := Generate(t.Context(), []Consumer{c}, Options{Extract: extract})
	if err == nil || !strings.Contains(err.Error(), "does not depend on holon-sh; run npm install holon-sh") {
		t.Fatalf("undeclared runtime = %v", err)
	}
	py := Consumer{Name: "py", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: c.Sources}
	err = Generate(t.Context(), []Consumer{py}, Options{Extract: extract})
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
	options := Options{Extract: fakeExtract(map[string]extraction{"example.com/orders": {Tools: []mcpcontract.Tool{lookupTool}}})}
	if err := Generate(t.Context(), consumers, options); err == nil || !strings.Contains(err.Error(), "install a separate holon-sh") {
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
	if err := Generate(t.Context(), consumers[1:], options); err == nil || !strings.Contains(err.Error(), "install a separate holon-sh") {
		t.Fatalf("a later project took over the slot: %v", err)
	}
}

func TestCheckTypeChecksTheProjectAgainstItsGeneratedClients(t *testing.T) {
	needNode(t)
	app := t.TempDir()
	pkg := filepath.Join(app, "node_modules/holon-sh")
	put(t, pkg, "package.json", `{"name":"holon-sh","exports":{".":{"types":"./generated/index.d.ts"}},"holon":{"slot":"generated","contractVersion":1}}`)
	put(t, pkg, "generated/holon-generated.json", string(mustJSON(t, slotMetadata{Marker: mcpcontract.Marker, App: filepath.Base(app)})))
	put(t, pkg, "generated/index.d.ts", `export interface Inputs { orders: { read: { id: string } } }`)
	compiler, err := filepath.Abs("../../tooling/typescript/node_modules/typescript")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(compiler, filepath.Join(app, "node_modules/typescript")); err != nil {
		t.Fatal(err)
	}
	put(t, app, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	put(t, app, "src/index.ts", `import type { Inputs } from "holon-sh"; const inputs: Inputs["orders"]["read"] = { id: "A-1" }; export default inputs;`)
	var output bytes.Buffer
	options := Options{Stdout: &output, Stderr: &output}
	if err := Check(t.Context(), app, options); err != nil {
		t.Fatalf("check: %v\n%s", err, &output)
	}
	put(t, app, "src/index.ts", `import type { Inputs } from "holon-sh"; const inputs: Inputs["orders"]["read"] = { id: 1 }; export default inputs;`)
	if err := Check(t.Context(), app, options); err == nil || !strings.Contains(err.Error(), "does not type-check") {
		t.Fatalf("check accepted a wrong argument type: %v\n%s", err, &output)
	}
}

func TestSuppliedContractsRemainDistinctAndValidated(t *testing.T) {
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
	if err := prepared.WritePythonSlot("caller", c.Dir); err != nil {
		t.Fatal(err)
	}
	if module := read(t, c.Dir, "holon/apps/__init__.py"); !strings.Contains(module, "async def lookup(") || !strings.Contains(module, "async def status(") {
		t.Fatal("pre-resolved contract did not generate a typed client")
	}
	invalid := &mcpcontract.Metadata{Identity: metadata.Identity, Schema: &mcpcontract.Contract{Tools: []mcpcontract.Tool{{Name: "bad", Input: json.RawMessage(`{"type":"string"}`)}}}}
	c.Sources = []Source{source, {Alias: "invalid", Document: document(t, invalid)}}
	if prepared, err := Prepare(t.Context(), []Consumer{c}, Options{}); err == nil {
		prepared.Close()
		t.Fatal("accepted an invalid contract after resolving a matching identity")
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
	if !strings.Contains(clients, `holon "github.com/holon-sh/sdk/go"`) || !strings.Contains(clients, " Lookup(") {
		t.Fatalf("Go client lacks the default runtime or dependency tool:\n%s", clients)
	}
}

func TestPythonSourcesWithTheRealExtractor(t *testing.T) {
	needPython(t)
	root := t.TempDir()
	orders, app := filepath.Join(root, "orders"), filepath.Join(root, "app")
	writeProvider(t, orders, "orders", pythonProvider)
	put(t, app, "requirements.txt", "holon-sh\n")
	c := Consumer{Name: "app", Dir: app, Targets: []string{TargetPy}, Sources: []Source{{Path: orders}}}
	if err := Generate(t.Context(), []Consumer{c}, Options{}); err != nil {
		t.Fatal(err)
	}
	module := read(t, app, "holon/apps/__init__.py")
	if !strings.Contains(module, "async def read(") || !strings.Contains(module, "orders") || !strings.HasPrefix(module, "# "+mcpcontract.Marker+" app=app\n") {
		t.Fatalf("module:\n%s", module)
	}
	contract, err := ResolveDocument(t.Context(), Source{Path: orders}, Options{})
	if err != nil || contract.Identity.Name != "example.com/orders" || len(contract.Schema.Tools) != 1 || contract.Schema.Tools[0].Name != "read" || len(contract.Requires) != 0 {
		t.Fatalf("ResolveDocument = %+v, %v", contract, err)
	}
}
