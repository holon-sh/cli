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

func checkoutConsumer(t *testing.T, targets ...string) (Consumer, func(context.Context, string, string, string, string) error, *[]string) {
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
	var calls []string
	c := Consumer{Name: "checkout", Dir: checkout, Targets: targets, Server: &Source{Path: filepath.Join(checkout, "server.ts")}, Sources: []Source{{Path: orders}}}
	return c, fakeExtract(t, results, &calls), &calls
}

func TestPrepareGeneratesNodeSlotsFromExplicitSources(t *testing.T) {
	c, extract, calls := checkoutConsumer(t, TargetTS)
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
	if got := prepared.Targets("checkout"); !slices.Equal(got, []string{TargetTS}) {
		t.Fatalf("targets = %v", got)
	}
	stage := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stage, "holon-sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = prepared.WriteNodeSlot("checkout", filepath.Join(stage, "holon-sh")); err != nil {
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
	var marker slotMetadata
	if err = readTypeJSON(filepath.Join(stage, "holon-sh/generated/holon-generated.json"), &marker); err != nil || marker.App != "checkout" {
		t.Fatalf("slot marker = %+v, %v", marker, err)
	}
	if strings.Contains(read(t, stage, "holon-sh/generated/holon-generated.json"), `"digests"`) {
		t.Fatal("slot marker contains unused contract digests")
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
		c := Consumer{Name: "app", Dir: app, Targets: []string{TargetTS}, Server: &Source{Path: filepath.Join(root, "orders")}}
		if _, err := Prepare(t.Context(), []Consumer{c}, options); err == nil || !strings.Contains(err.Error(), "is outside") {
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

func TestPrepareRejectsInvalidExtractedContracts(t *testing.T) {
	for _, server := range []string{"example.com/checkout", "example.com/orders"} {
		t.Run(server, func(t *testing.T) {
			c, _, calls := checkoutConsumer(t, TargetTS)
			results := map[string]extraction{
				"example.com/checkout": {Tools: []mcpcontract.Tool{lookupTool}},
				"example.com/orders":   {Tools: []mcpcontract.Tool{lookupTool}},
			}
			results[server] = extraction{Tools: []mcpcontract.Tool{{Name: "invalid", Input: json.RawMessage(`{"type":"string"}`)}}}
			if prepared, err := Prepare(t.Context(), []Consumer{c}, Options{Extract: fakeExtract(t, results, calls)}); err == nil {
				prepared.Close()
				t.Fatal("accepted an invalid extracted contract")
			}
		})
	}
}

func TestGenerateWritesSlotsAndRequiresDeclaredRuntimes(t *testing.T) {
	c, extract, _ := checkoutConsumer(t, TargetTS)
	node := slotPackage(t, c.Dir, "holon-sh")
	var output bytes.Buffer
	if err := Generate(t.Context(), []Consumer{c}, Options{Stdout: &output, Extract: extract}); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	if got, want := output.String(), "checkout: generated ts types\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	var marker slotMetadata
	if err := readTypeJSON(filepath.Join(node, "generated/holon-generated.json"), &marker); err != nil || marker.App != "checkout" || marker.Placeholder {
		t.Fatalf("%s marker = %+v, %v", node, marker, err)
	}
	orders := c.Sources[0].Path
	if entries, _ := os.ReadDir(orders); len(entries) != 2 {
		t.Fatalf("source directory changed: %v", entries)
	}

	put(t, c.Dir, "package.json", `{"type":"module"}`)
	if err := os.RemoveAll(node); err != nil {
		t.Fatal(err)
	}
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

func TestGenerateInstallsADeclaredRuntime(t *testing.T) {
	c, extract, _ := checkoutConsumer(t, TargetTS)
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

func TestPreResolvedContractSource(t *testing.T) {
	metadata := ordersMetadata()
	source := Source{Contract: metadata}
	resolved, err := Resolve(t.Context(), source, Options{})
	if err != nil || resolved != metadata {
		t.Fatalf("Resolve = %+v, %v", resolved, err)
	}
	alternate := ordersMetadata()
	alternate.Contract = &mcpcontract.Contract{Tools: []mcpcontract.Tool{{Name: "status", Input: json.RawMessage(`{"type":"object"}`)}}}
	c := Consumer{Name: "caller", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: []Source{source, {Alias: "alternate", Contract: alternate}}}
	prepared, err := Prepare(t.Context(), []Consumer{c}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if got := prepared.Metadata(); got["orders"] != metadata || got["alternate"] != alternate || len(got["caller"].Requires["orders"].Contract.Tools) != 1 {
		t.Fatalf("resolved contracts = %+v", got)
	}
	if err := prepared.WritePythonSlot("caller", c.Dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, c.Dir, "holon/apps/__init__.py"), "async def lookup(") || !strings.Contains(read(t, c.Dir, "holon/apps/__init__.py"), "async def status(") {
		t.Fatal("pre-resolved contract did not generate a typed client")
	}
	for _, invalid := range []*mcpcontract.Metadata{
		{Version: 99, Identity: metadata.Identity, Contract: metadata.Contract},
		{Version: mcpcontract.Version, Contract: metadata.Contract},
		{Version: mcpcontract.Version, Identity: metadata.Identity, Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{{Name: "bad", Input: json.RawMessage(`{"type":"string"}`)}}}},
	} {
		if _, err := Resolve(t.Context(), Source{Contract: invalid}, Options{}); err == nil {
			t.Fatalf("accepted invalid contract: %+v", invalid)
		}
		c.Sources = []Source{source, {Alias: "invalid", Contract: invalid}}
		if prepared, err := Prepare(t.Context(), []Consumer{c}, Options{}); err == nil {
			prepared.Close()
			t.Fatalf("accepted invalid contract after resolving a matching identity: %+v", invalid)
		}
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
		noContract bool
		want       string
	}{
		{name: "grant ahead of docker", credential: grant("images.example", "secret"), env: []string{"DOCKER_CONFIG=" + docker("wrong")}},
		{name: "docker for other registries", credential: grant("elsewhere.example", "wrong"), env: []string{"DOCKER_CONFIG=" + docker("secret")}},
		{name: "docker under HOME", env: []string{"HOME=" + filepath.Dir(docker("secret")), "DOCKER_CONFIG="}},
		{name: "malformed environment", env: []string{"INVALID"}, want: "must be KEY=VALUE"},
		{name: "rejected grant", credential: grant("images.example", "wrong"), env: []string{"DOCKER_CONFIG=" + docker("secret")}, want: "rejected the supplied registry credential"},
		{name: "rejected docker credential", env: []string{"DOCKER_CONFIG=" + docker("wrong")}, want: "images.example/orders:1.0.0"},
		{name: "image without a contract", env: []string{"DOCKER_CONFIG=" + docker("secret")}, noContract: true, want: "carries no MCP contract artifact"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := append([]string{"DOCKER_CONFIG=" + t.TempDir()}, test.env...)
			metadata := ordersMetadata()
			if test.noContract {
				metadata = nil
			}
			c := Consumer{Name: "caller", Dir: t.TempDir(), Targets: []string{TargetPy}, Sources: []Source{{Image: "images.example/orders:1.0.0"}}}
			prepared, err := Prepare(t.Context(), []Consumer{c}, Options{HTTPClient: authenticatedRegistry(t, metadata), Env: env, Credential: test.credential})
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
			if resolved["caller"].Requires["orders"].Identity != ordersMetadata().Identity || resolved["orders"] == nil {
				t.Fatalf("image source not resolved: %+v", resolved)
			}
		})
	}
}

func TestGoConsumerClients(t *testing.T) {
	root := t.TempDir()
	put(t, root, "go.mod", "module example.com/caller\n\ngo 1.27.0\n")
	put(t, root, "server.go", "package main\n\nfunc main() {}\n")
	put(t, root, "server.json", `{"name":"example.com/caller","version":"1.0.0","description":"Caller"}`)
	c := Consumer{Name: "caller", Dir: root, Targets: []string{TargetGo}, Server: &Source{Path: root}, Sources: []Source{{Contract: ordersMetadata()}}}
	options := Options{GoModule: "github.com/holon-sh/sdk/go",
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
	if !strings.Contains(module, "async def read(") || !strings.Contains(module, "orders") || !strings.HasPrefix(module, "# "+mcpcontract.Marker+" app=app\n") {
		t.Fatalf("module:\n%s", module)
	}
	contract, err := Resolve(t.Context(), Source{Path: orders}, Options{})
	if err != nil || contract.Identity.Name != "example.com/orders" || len(contract.Contract.Tools) != 1 || contract.Contract.Tools[0].Name != "read" || len(contract.Requires) != 0 {
		t.Fatalf("Resolve = %+v, %v", contract, err)
	}
}
