package typegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// checkoutConsumer writes a TypeScript checkout server beside a Python orders server and returns
// the consumer plus a fake extractor answering both.
func checkoutConsumer(t *testing.T, targets ...string) (Consumer, func(context.Context, string, string, string, string) error, *[]string) {
	t.Helper()
	root := t.TempDir()
	checkout, orders := filepath.Join(root, "checkout"), filepath.Join(root, "orders")
	writeProvider(t, orders, "orders", pythonProvider)
	put(t, checkout, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1","holon-ui":"^0.0.1"}}`)
	put(t, checkout, "server.ts", "export default {};\n")
	put(t, checkout, "server.json", string(mustJSON(t, map[string]any{"name": "example.com/checkout", "version": "1.0.0", "description": "Checkout"})))
	own := mcpcontract.Tool{Name: "status", Input: json.RawMessage(`{"type":"object","additionalProperties":false}`)}
	results := map[string]extraction{
		"example.com/checkout": {Tools: []mcpcontract.Tool{own}, Sources: map[string]string{"server.ts": "sha256:checkout"}},
		"example.com/orders":   {Tools: []mcpcontract.Tool{lookupTool}},
	}
	var calls []string
	c := Consumer{Name: "checkout", Dir: checkout, Targets: targets, Server: &Source{Path: filepath.Join(checkout, "server.ts")}, Sources: []Source{{Path: orders}}}
	return c, fakeExtract(t, results, &calls), &calls
}

func TestPrepareGeneratesEveryTargetFromExplicitSources(t *testing.T) {
	c, extract, calls := checkoutConsumer(t, TargetUI, TargetTS, TargetPy)
	prepared, err := Prepare(t.Context(), []Consumer{c}, Options{Extract: extract})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if !slices.Equal(*calls, []string{"python", "typescript"}) {
		t.Fatalf("extractor calls = %v, want one batch per language", *calls)
	}
	metadata := prepared.Metadata()["checkout"]
	if metadata.Identity.Name != "example.com/checkout" || metadata.Contract == nil || len(metadata.Requires["orders"].Contract.Tools) != 1 {
		t.Fatalf("consumer metadata = %+v", metadata)
	}
	if got := prepared.Targets("checkout"); !slices.Equal(got, []string{TargetUI, TargetTS, TargetPy}) {
		t.Fatalf("targets = %v", got)
	}
	stage := t.TempDir()
	for _, pkg := range []string{"holon-sh", "holon-ui"} {
		if err := os.MkdirAll(filepath.Join(stage, pkg), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err = prepared.WriteNodeSlot("checkout", filepath.Join(stage, "holon-sh")); err != nil {
		t.Fatal(err)
	}
	if err = prepared.WriteBrowserSlot("checkout", filepath.Join(stage, "holon-ui")); err != nil {
		t.Fatal(err)
	}
	if err = prepared.WritePythonSlot("checkout", stage); err != nil {
		t.Fatal(err)
	}
	node := read(t, stage, "holon-sh/generated/index.js")
	for _, want := range []string{`import { createApps as create, bindApps } from "../runtime.mjs";`, `"orders":{"tools":[{"name":"lookup"`} {
		if !strings.Contains(node, want) {
			t.Fatalf("holon-sh index.js lacks %s:\n%s", want, node)
		}
	}
	if strings.Contains(node, `"status"`) {
		t.Fatal("the ts slot received the consumer's own contract")
	}
	browser := read(t, stage, "holon-ui/generated/index.js")
	declarations := read(t, stage, "holon-ui/generated/index.d.ts")
	for _, want := range []string{`export const App = appClass(identity, contracts, "checkout");`, `"checkout":{"tools":[{"name":"status"`, `"orders":{"tools":[{"name":"lookup"`} {
		if !strings.Contains(browser, want) {
			t.Fatalf("holon-ui index.js lacks %s:\n%s", want, browser)
		}
	}
	if !strings.Contains(declarations, `export declare class App extends HostApp { readonly tools: Apps["checkout"]["tools"];`) {
		t.Fatalf("holon-ui index.d.ts lacks the App class:\n%s", declarations)
	}
	if module := read(t, stage, "holon/apps/__init__.py"); !strings.Contains(module, "async def lookup(") || !strings.HasPrefix(module, "# "+mcpcontract.Marker+" app=checkout\n") {
		t.Fatalf("python module:\n%s", module)
	}
	var marker slotMetadata
	if err = readTypeJSON(filepath.Join(stage, "holon-ui/generated/holon-generated.json"), &marker); err != nil || marker.App != "checkout" || marker.Digests["checkout"] == "" || marker.Digests["orders"] == "" {
		t.Fatalf("slot marker = %+v, %v", marker, err)
	}
	config, ok := prepared.CheckConfig("checkout")
	if want := `{"check":true,"entry":"server.ts","sources":{"server.ts":"sha256:checkout"}}`; !ok || string(mustJSON(t, config)) != want {
		t.Fatalf("CheckConfig = %s %t, want %s", mustJSON(t, config), ok, want)
	}
	if _, ok := prepared.CheckConfig("missing"); ok {
		t.Fatal("unknown consumer has a check configuration")
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
	var calls []string
	options := Options{Extract: fakeExtract(t, results, &calls)}
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
		c := Consumer{Name: "app", Dir: app, Targets: []string{TargetUI}, Server: &Source{Path: filepath.Join(root, "orders")}}
		if _, err := Prepare(t.Context(), []Consumer{c}, options); err == nil || !strings.Contains(err.Error(), "is outside") {
			t.Fatalf("Prepare = %v", err)
		}
	})
	t.Run("own alias colliding with a source", func(t *testing.T) {
		c, extract, _ := checkoutConsumer(t, TargetUI)
		c.Sources[0].Alias = "checkout"
		if _, err := Prepare(t.Context(), []Consumer{c}, Options{Extract: extract}); err == nil || !strings.Contains(err.Error(), "both become checkout") {
			t.Fatalf("Prepare = %v", err)
		}
	})
	t.Run("extractor failure", func(t *testing.T) {
		failure := errors.New("buildx unavailable")
		c, _, _ := checkoutConsumer(t, TargetTS)
		if _, err := Prepare(t.Context(), []Consumer{c}, Options{Extract: func(context.Context, string, string, string, string) error { return failure }}); !errors.Is(err, failure) {
			t.Fatalf("extractor failure = %v, want %v", err, failure)
		}
	})
}

func TestGenerateWritesSlotsAndRequiresDeclaredRuntimes(t *testing.T) {
	c, extract, _ := checkoutConsumer(t, TargetUI, TargetTS)
	node, ui := slotPackage(t, c.Dir, "holon-sh"), slotPackage(t, c.Dir, "holon-ui")
	var output bytes.Buffer
	if err := Generate(t.Context(), []Consumer{c}, Options{Stdout: &output, Extract: extract}); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	if got, want := output.String(), "checkout: generated ui types\ncheckout: generated ts types\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	for _, pkg := range []string{node, ui} {
		var marker slotMetadata
		if err := readTypeJSON(filepath.Join(pkg, "generated/holon-generated.json"), &marker); err != nil || marker.App != "checkout" || marker.Placeholder {
			t.Fatalf("%s marker = %+v, %v", pkg, marker, err)
		}
	}
	if !strings.Contains(read(t, ui, "generated/index.js"), `appClass(identity, contracts, "checkout")`) {
		t.Fatal("holon-ui slot has no App class")
	}
	// Orders, a source, is left exactly as it was.
	orders := c.Sources[0].Path
	if entries, _ := os.ReadDir(orders); len(entries) != 2 {
		t.Fatalf("source directory changed: %v", entries)
	}

	// An undeclared slot package is an error naming the install command; a declared one is installed.
	put(t, c.Dir, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	if err := os.RemoveAll(ui); err != nil {
		t.Fatal(err)
	}
	err := Generate(t.Context(), []Consumer{c}, Options{Extract: extract})
	if err == nil || !strings.Contains(err.Error(), "does not depend on holon-ui; run npm install holon-ui") {
		t.Fatalf("undeclared runtime = %v", err)
	}
	py := Consumer{Name: "py", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: c.Sources}
	err = Generate(t.Context(), []Consumer{py}, Options{Extract: extract})
	if err == nil || !strings.Contains(err.Error(), "does not depend on holon-sh; run uv add holon-sh") {
		t.Fatalf("undeclared python runtime = %v", err)
	}
	put(t, py.Dir, "requirements.txt", "holon-sh\n")
	if err = Generate(t.Context(), []Consumer{py}, Options{Extract: extract}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, py.Dir, "holon/apps/__init__.py"), "async def lookup(") {
		t.Fatal("python slot not written")
	}
}

func TestGenerateInstallsADeclaredRuntime(t *testing.T) {
	c, extract, _ := checkoutConsumer(t, TargetTS)
	// A fake npm on PATH records the install and creates the package.
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	put(t, bin, "npm", "#!/bin/sh\necho \"$PWD $* $INSTALL_MODE\" >> "+log+"\nmkdir -p node_modules/holon-sh/generated\nprintf '%s' '{\"holon\":{\"slot\":\"generated\",\"contractVersion\":1}}' > node_modules/holon-sh/package.json\n")
	if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INSTALL_MODE", "inherited")
	var output bytes.Buffer
	if err := Generate(t.Context(), []Consumer{c}, Options{Stderr: &output, Extract: extract, Env: []string{"INSTALL_MODE=first", "INSTALL_MODE=last=value"}}); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	if calls := read(t, bin, "calls"); calls != c.Dir+" install last=value\n" {
		t.Fatalf("npm calls = %q", calls)
	}
	if !isFile(filepath.Join(c.Dir, "node_modules/holon-sh/generated/index.js")) {
		t.Fatal("slot not written after installing")
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
	var calls []string
	err := Generate(t.Context(), consumers, Options{Extract: fakeExtract(t, map[string]extraction{"example.com/orders": {Tools: []mcpcontract.Tool{lookupTool}}}, &calls)})
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
	if !strings.Contains(output.String(), "Typed contracts check passed.") {
		t.Fatalf("output = %q", &output)
	}
	put(t, app, "src/index.ts", `import type { Inputs } from "holon-sh"; const inputs: Inputs["orders"]["read"] = { id: 1 }; export default inputs;`)
	output.Reset()
	if err := Check(t.Context(), app, options); err == nil || !strings.Contains(err.Error(), "does not type-check") || !strings.Contains(output.String(), "TS2322") {
		t.Fatalf("check accepted a wrong argument type: %v\n%s", err, &output)
	}
}

func TestCredentialAuthenticatesImageSources(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	grant := func(host, password string) auth.CredentialFunc {
		return auth.StaticCredential(host, auth.Credential{Username: "user", Password: password})
	}
	docker := func(password string) string {
		dir := filepath.Join(t.TempDir(), "home=override", ".docker")
		encoded := base64.StdEncoding.EncodeToString([]byte("user:" + password))
		put(t, dir, "config.json", `{"auths":{"images.example":{"auth":"`+encoded+`"}}}`)
		return dir
	}
	for _, test := range []struct {
		name       string
		credential auth.CredentialFunc
		env        []string
		want       string
	}{
		{name: "grant", credential: grant("images.example", "secret")},
		{name: "grant ahead of docker", credential: grant("images.example", "secret"), env: []string{"DOCKER_CONFIG=" + docker("wrong")}},
		{name: "docker for other registries", credential: grant("elsewhere.example", "wrong"), env: []string{"DOCKER_CONFIG=" + docker("secret")}},
		{name: "docker under HOME", env: []string{"HOME=" + filepath.Dir(docker("secret")), "DOCKER_CONFIG="}},
		{name: "malformed environment", env: []string{"INVALID"}, want: "must be KEY=VALUE"},
		{name: "rejected grant", credential: grant("images.example", "wrong"), env: []string{"DOCKER_CONFIG=" + docker("secret")}, want: "rejected the supplied registry credential"},
		{name: "rejected docker credential", env: []string{"DOCKER_CONFIG=" + docker("wrong")}, want: "images.example/orders:1.0.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := append([]string{"DOCKER_CONFIG=" + t.TempDir()}, test.env...)
			c := Consumer{Name: "caller", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: []Source{{Image: "images.example/orders:1.0.0"}}}
			prepared, err := Prepare(t.Context(), []Consumer{c}, Options{HTTPClient: authenticatedRegistry(t, ordersMetadata()), Env: env, Credential: test.credential})
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
			metadata := prepared.Metadata()
			if metadata["caller"].Requires["orders"].Identity != ordersMetadata().Identity || metadata["orders"] == nil {
				t.Fatalf("image source not resolved: %+v", metadata)
			}
		})
	}
}

func TestGoConsumerClients(t *testing.T) {
	root := t.TempDir()
	put(t, root, "go.mod", "module example.com/caller\n\ngo 1.27.0\n")
	put(t, root, "server.go", "package main\n\nfunc main() {}\n")
	put(t, root, "server.json", `{"name":"example.com/caller","version":"1.0.0","description":"Caller"}`)
	c := Consumer{Name: "caller", Dir: root, Targets: []string{TargetGo}, Server: &Source{Path: root}, Sources: []Source{{Image: "images.example/orders:1.0.0"}}}
	options := Options{HTTPClient: imageClient(t, ordersMetadata(), mcpcontract.MediaType), Env: []string{"DOCKER_CONFIG=" + t.TempDir()}, GoModule: "github.com/holon-sh/sdk/go",
		Extract: func(context.Context, string, string, string, string) error {
			return errors.New("Go servers are not extracted")
		}}
	prepared, err := Prepare(t.Context(), []Consumer{c}, options)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	metadata := prepared.Metadata()["caller"]
	if metadata.Contract != nil || metadata.Identity.Name != "example.com/caller" || len(metadata.Requires["orders"].Contract.Tools) != 1 {
		t.Fatalf("Go consumer metadata = %+v", metadata)
	}
	if _, ok := prepared.CheckConfig("caller"); ok {
		t.Fatal("Go consumers have no runner check")
	}
	stage := t.TempDir()
	if err = prepared.WriteGoSlot("caller", stage); err != nil {
		t.Fatal(err)
	}
	// Compiler goldens in mcptype cover emitted syntax; this boundary supplies the runtime,
	// contract and ownership marker to that compiler and writes the result into holon/.
	clients := read(t, stage, "holon/clients.go")
	if !strings.Contains(clients, `holon "`+options.GoModule+`"`) || !strings.Contains(clients, " Lookup(") ||
		!strings.Contains(read(t, stage, "holon/types.go"), "type OrdersLookupInput") || !mcpcontract.Managed(filepath.Join(stage, "holon")) {
		t.Fatalf("Go slot lacks the configured runtime, typed client or ownership marker:\n%s", clients)
	}
	var output bytes.Buffer
	options.Stdout = &output
	if err = Generate(t.Context(), []Consumer{c}, options); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if output.String() != "caller: generated go types\n" {
		t.Fatalf("generate output = %q", &output)
	}
	other := t.TempDir()
	put(t, other, "holon/mine.go", "package holon\n")
	if err = prepared.WriteGoSlot("caller", other); err == nil || !strings.Contains(err.Error(), "unmarked") {
		t.Fatalf("overwrote a user holon/ package: %v", err)
	}
	if _, err := Prepare(t.Context(), []Consumer{c}, Options{HTTPClient: options.HTTPClient, Env: options.Env}); err == nil || !strings.Contains(err.Error(), "GoModule") {
		t.Fatalf("go target without GoModule = %v", err)
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
	if !strings.Contains(module, "async def read(") || !strings.Contains(module, "orders") {
		t.Fatalf("module:\n%s", module)
	}
	contract, err := Resolve(t.Context(), Source{Path: orders}, Options{})
	if err != nil || contract.Identity.Name != "example.com/orders" || len(contract.Contract.Tools) != 1 || contract.Contract.Tools[0].Name != "read" || len(contract.Requires) != 0 {
		t.Fatalf("Resolve = %+v, %v", contract, err)
	}
}
