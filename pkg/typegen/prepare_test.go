package typegen

import (
	"bytes"
	"context"
	"encoding/base64"
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
	node := read(t, stage, "holon-sh/generated/index.js")
	for _, want := range []string{`import { createApps as create, bindApps } from "../runtime.mjs";`, `"name":"lookup"`} {
		if !strings.Contains(node, want) {
			t.Fatalf("holon-sh index.js lacks %s:\n%s", want, node)
		}
	}
	if strings.Contains(node, `"status"`) {
		t.Fatal("the ts slot received the consumer's own contract")
	}
	browser := read(t, stage, "holon-sh/generated/browser.js")
	for _, want := range []string{`from "../browser-runtime.mjs"`, `"status"`, `"lookup"`} {
		if !strings.Contains(browser, want) {
			t.Fatalf("browser client lacks %s: %s", want, browser)
		}
	}
	for _, test := range []struct{ consumer, want string }{{"missing", "unknown consumer"}, {"checkout", "no go clients"}} {
		if err := prepared.WriteGoSlot(test.consumer, t.TempDir()); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("WriteGoSlot(%s) = %v, want %q", test.consumer, err, test.want)
		}
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
	options := Options{Extract: fakeExtract(results)}
	source := func(name string) Source { return Source{Path: filepath.Join(root, name)} }
	for _, test := range []struct {
		name    string
		sources []Source
		want    string
	}{
		{"missing server.json", []Source{source("nameless")}, "needs a server.json"},
		{"extraction diagnostic", []Source{source("broken")}, "tools are registered dynamically"},
		{"invalid derived alias", []Source{source("odd")}, `cannot derive a client name from "example.com/---"; write ALIAS=`},
		{"duplicate alias", []Source{source("orders"), source("orders-copy")}, "both become apps.orders"},
		{"explicit alias fixes duplicates", []Source{source("orders"), {Alias: "backup", Path: filepath.Join(root, "orders-copy")}}, ""},
		{"explicit alias fixes derivation", []Source{{Alias: "odd", Path: filepath.Join(root, "odd")}}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared, err := Prepare(t.Context(), []Consumer{{Name: "app", Dir: app, Targets: []string{TargetTS}, Sources: test.sources}}, options)
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
	t.Run("own server outside the consumer", func(t *testing.T) {
		c := Consumer{Name: "app", Dir: app, Targets: []string{TargetTS}, Server: &Source{Path: filepath.Join(root, "orders")}}
		if _, err := Prepare(t.Context(), []Consumer{c}, options); err == nil || !strings.Contains(err.Error(), "is outside") {
			t.Fatalf("Prepare = %v", err)
		}
	})
	t.Run("extractor failure", func(t *testing.T) {
		failure := errors.New("buildx unavailable")
		c, _ := checkoutConsumer(t, TargetTS)
		if _, err := Prepare(t.Context(), []Consumer{c}, Options{Extract: func(context.Context, string, string, string, string) error { return failure }}); !errors.Is(err, failure) {
			t.Fatalf("extractor failure = %v, want %v", err, failure)
		}
	})
}

func TestPrepareRejectsInvalidExtractedContracts(t *testing.T) {
	for _, server := range []string{"example.com/checkout", "example.com/orders"} {
		t.Run(server, func(t *testing.T) {
			c, _ := checkoutConsumer(t, TargetTS)
			results := map[string]extraction{
				"example.com/checkout": {Tools: []mcpcontract.Tool{lookupTool}},
				"example.com/orders":   {Tools: []mcpcontract.Tool{lookupTool}},
			}
			results[server] = extraction{Tools: []mcpcontract.Tool{{Name: "invalid", Input: json.RawMessage(`{"type":"string"}`)}}}
			if prepared, err := Prepare(t.Context(), []Consumer{c}, Options{Extract: fakeExtract(results)}); err == nil {
				prepared.Close()
				t.Fatal("accepted an invalid extracted contract")
			}
		})
	}
}

func TestGenerateInstallsADeclaredRuntime(t *testing.T) {
	c, extract := checkoutConsumer(t, TargetTS)
	bin := t.TempDir()
	put(t, bin, "npm", "#!/bin/sh\nset -e\n[ \"$HOLON_SKIP_POSTINSTALL\" = 1 ]\n[ \"$INSTALL_MODE\" = last=value ]\nprintf '{}' > package-lock.json\nmkdir -p node_modules/holon-sh/generated\nprintf '%s' '{\"holon\":{\"slot\":\"generated\",\"contractVersion\":1}}' > node_modules/holon-sh/package.json\n")
	if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INSTALL_MODE", "inherited")
	var output bytes.Buffer
	if err := Generate(t.Context(), []Consumer{c}, Options{Stdout: &output, Stderr: &output, Extract: extract, Env: []string{"INSTALL_MODE=first", "INSTALL_MODE=last=value"}}); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	var marker slotMetadata
	if err := readTypeJSON(filepath.Join(c.Dir, "node_modules/holon-sh/generated/holon-generated.json"), &marker); err != nil || marker.App != "checkout" || marker.Placeholder {
		t.Fatalf("slot marker = %+v, %v", marker, err)
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

func TestGenerateRejectsASharedNodeSlotBeforeWritingEitherConsumer(t *testing.T) {
	root := t.TempDir()
	pkg := slotPackage(t, root, "holon-sh")
	put(t, pkg, "generated/index.js", "placeholder")
	writeProvider(t, filepath.Join(root, "orders"), "orders", pythonProvider)
	var consumers []Consumer
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(root, name)
		put(t, dir, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
		consumers = append(consumers, Consumer{Name: name, Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Path: filepath.Join(root, "orders")}}})
	}
	err := Generate(t.Context(), consumers, Options{Extract: fakeExtract(map[string]extraction{"example.com/orders": {Tools: []mcpcontract.Tool{lookupTool}}})})
	if err == nil || !strings.Contains(err.Error(), "share") {
		t.Fatalf("shared slot not diagnosed: %v", err)
	}
	if got := read(t, pkg, "generated/index.js"); got != "placeholder" {
		t.Fatal("shared slot changed before rejection")
	}
}

func TestCheckTypeChecksTheProjectAgainstItsGeneratedClients(t *testing.T) {
	needNode(t)
	app := t.TempDir()
	pkg := slotPackage(t, app, "holon-sh")
	put(t, app, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	put(t, app, "src/index.ts", `import type { Inputs } from "holon-sh"; const inputs: Inputs["orders"]["read"] = { id: "A-1" }; export default inputs;`)
	var output bytes.Buffer
	options := Options{Stdout: &output, Stderr: &output}
	if err := Check(t.Context(), app, options); err == nil || !strings.Contains(err.Error(), "no generated clients; run holon types generate") {
		t.Fatalf("check of a placeholder slot = %v", err)
	}
	put(t, pkg, "package.json", `{"name":"holon-sh","exports":{".":{"types":"./generated/index.d.ts"}},"holon":{"slot":"generated","contractVersion":1}}`)
	put(t, pkg, "generated/holon-generated.json", string(mustJSON(t, slotMetadata{Marker: mcpcontract.Marker, App: filepath.Base(app)})))
	put(t, pkg, "generated/index.d.ts", `export interface Inputs { orders: { read: { id: string } } }`)
	if err := Check(t.Context(), app, options); err != nil {
		t.Fatalf("check: %v\n%s", err, &output)
	}
	put(t, app, "src/index.ts", `import type { Inputs } from "holon-sh"; const inputs: Inputs["orders"]["read"] = { id: 1 }; export default inputs;`)
	output.Reset()
	if err := Check(t.Context(), app, options); err == nil || !strings.Contains(err.Error(), "does not type-check") || !strings.Contains(output.String(), "TS2322") {
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
	if !strings.Contains(read(t, c.Dir, "holon/apps/__init__.py"), "async def lookup(") || !strings.Contains(read(t, c.Dir, "holon/apps/__init__.py"), "async def status(") {
		t.Fatal("pre-resolved contract did not generate a typed client")
	}
	for _, invalid := range []*mcpcontract.Metadata{
		{Identity: metadata.Identity, Requires: map[string]string{"orders": "not-a-digest"}},
		{Schema: metadata.Schema},
		{Identity: metadata.Identity, Schema: &mcpcontract.Contract{Tools: []mcpcontract.Tool{{Name: "bad", Input: json.RawMessage(`{"type":"string"}`)}}}},
	} {
		c.Sources = []Source{source, {Alias: "invalid", Document: document(t, invalid)}}
		if prepared, err := Prepare(t.Context(), []Consumer{c}, Options{}); err == nil {
			prepared.Close()
			t.Fatalf("accepted invalid contract after resolving a matching identity: %+v", invalid)
		}
	}
}

func TestDockerCredentialsAuthenticateRegistryPackages(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	docker := func(password string) string {
		dir := filepath.Join(t.TempDir(), "home=override", ".docker")
		encoded := base64.StdEncoding.EncodeToString([]byte("user:" + password))
		put(t, dir, "config.json", `{"auths":{"images.example":{"auth":"`+encoded+`"}}}`)
		return dir
	}
	for _, test := range []struct {
		name string
		env  []string
		want string
	}{
		{name: "docker config", env: []string{"DOCKER_CONFIG=" + docker("secret")}},
		{name: "docker under HOME", env: []string{"HOME=" + filepath.Dir(docker("secret")), "DOCKER_CONFIG="}},
		{name: "malformed environment", env: []string{"INVALID"}, want: "must be KEY=VALUE"},
		{name: "rejected docker credential", env: []string{"DOCKER_CONFIG=" + docker("wrong")}, want: "images.example/orders:1.0.0"},
		{name: "image without a contract", env: []string{"DOCKER_CONFIG=" + docker("secret")}, want: "publishes neither an OCI package with a contract nor a streamable-http remote"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := append([]string{"DOCKER_CONFIG=" + t.TempDir()}, test.env...)
			metadata := ordersMetadata()
			if test.name == "image without a contract" {
				metadata = nil
			}
			source, client := registryImageSource(t, authenticatedRegistry(t, metadata))
			c := Consumer{Name: "caller", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: []Source{source}}
			prepared, err := Prepare(t.Context(), []Consumer{c}, Options{HTTPClient: client, Env: env})
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
			resolved := prepared.Metadata()
			if prepared.Documents()[resolved["caller"].Requires["orders"]].Identity != ordersMetadata().Identity || resolved["orders"] == nil {
				t.Fatalf("registry package not resolved: %+v", resolved)
			}
		})
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
	if err := os.RemoveAll(filepath.Join(root, "holon")); err != nil {
		t.Fatal(err)
	}
	put(t, root, "holon/mine.go", "package holon\n")
	if err := Generate(t.Context(), []Consumer{c}, Options{}); err == nil || !strings.Contains(err.Error(), "unmarked") {
		t.Fatalf("overwrote a user holon/ package: %v", err)
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
