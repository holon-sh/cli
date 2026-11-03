//go:build docker

package cloud

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/typegen"

	"github.com/holon-sh/cli/pkg/protocol"
)

func TestDockerAutomaticTypes(t *testing.T) {
	root := t.TempDir()
	const pkg = `{"type":"module","engines":{"node":">=22.20.0 <23"},
"dependencies":{"@modelcontextprotocol/server":"2.3.1","holon-sh":"^0.0.1-dev.2","holon-ui":"^0.0.1-dev.0","zod":"4.6.5"},
"devDependencies":{"typescript":"7.0.2"},
"scripts":{"build":"node --input-type=module -e \"import {apps} from 'holon-sh'; import {App} from 'holon-ui'; ` +
		`if (!apps.orders || typeof App !== 'function') throw Error('missing generated import')\""}}`
	const config = `{"compilerOptions":{"target":"ES2023","module":"NodeNext","moduleResolution":"NodeNext",
"strict":true,"skipLibCheck":true,"noEmit":true},"include":["server.ts"]}`
	provider := `import {McpServer,createMcpHandler} from "@modelcontextprotocol/server"; import {z} from "zod";
export default createMcpHandler(() => {
  const server=new McpServer({name:"orders",version:"1"});
  server.registerTool("get_order", {inputSchema:z.object({id:z.string()}),outputSchema:z.object({total:z.number()})},
    ({id}) => ({content:[{type:"text",text:id}],structuredContent:{total:5}}));
  return server;
});`
	caller := `import {McpServer,createMcpHandler} from "@modelcontextprotocol/server"; import {z} from "zod"; import {apps} from "holon-sh";
export default createMcpHandler(() => {
  const server=new McpServer({name:"checkout",version:"1"});
  server.registerTool("checkout", {inputSchema:z.object({id:z.string()})}, async ({id}) => {
    const result=await apps.orders.tools.get_order({id});
    result.total.toFixed();
    return {content:[],structuredContent:result};
  });
  return server;
});`
	manifest := manifestSpec{}
	originalFiles := map[string][]byte{}
	for _, name := range []string{"orders", "checkout"} {
		path := filepath.Join(root, name)
		code := provider
		if name == "checkout" {
			code = caller
		}
		var packageJSON string
		lockName := "package-lock.json"
		if name == "checkout" {
			packageJSON = strings.Replace(pkg, `"type":"module"`, `"type":"module","packageManager":"pnpm@10.13.1"`, 1)
			lockName = "pnpm-lock.yaml"
		} else {
			var document map[string]any
			if err := json.Unmarshal([]byte(pkg), &document); err != nil {
				t.Fatal(err)
			}
			delete(document, "scripts")
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			packageJSON = string(data)
		}
		lock, err := os.ReadFile(filepath.Join("testdata", "automatic-types", lockName))
		if err != nil {
			t.Fatal(err)
		}
		sourceFiles(t, path, map[string]string{"package.json": packageJSON, lockName: string(lock), "tsconfig.json": config, "server.ts": code})
		installRuntime(t, path)
		sourceFiles(t, path, map[string]string{
			"node_modules/holon-ui/package.json":                   `{"name":"holon-ui","holon":{"slot":"generated","contractVersion":1}}`,
			"node_modules/holon-ui/generated/holon-generated.json": `{"marker":"` + mcpcontract.Marker + `","placeholder":true}`,
		})
		originalFiles[filepath.Join(path, "package.json")] = []byte(packageJSON)
		originalFiles[filepath.Join(path, lockName)] = lock
		descriptor := &serverDescriptor{Name: "io.github.example/" + name, Version: "1.0.0"}
		app := manifestApp{Name: name, Path: path, resolved: resolvedApp{App: protocol.App{Name: name, Port: 3000}, descriptor: descriptor}}
		if name == "checkout" {
			descriptor.Requires = map[string]serverReference{
				"orders": {Name: "io.github.example/orders", Version: "1.0.0", Registry: "https://private-registry.example"},
			}
			app.resolved.Requires = []string{"orders"}
		}
		manifest.Apps = append(manifest.Apps, app)
	}
	manifest = typedManifest(t, manifest.Apps...)
	_, grant, options := sourceRegistry(t)
	options.check = true
	var diagnostics strings.Builder
	options.Stderr = &buildLog{writer: &diagnostics}
	grant.Repositories = map[string]string{"orders": "apps/orders", "checkout": "apps/checkout"}
	options.architectures = []string{"amd64", "arm64"}
	options.Env = append(options.Env, "DOCKER_CONTEXT="+os.Getenv("DOCKER_CONTEXT"))
	editor := func(app manifestApp, name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(app.Path, "node_modules/holon-sh/generated", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	workspace := new(typeWorkspace)
	if err := workspace.acquire(manifest); err != nil {
		t.Fatal(err)
	}
	defer workspace.close()
	options.types = workspace
	prepared, cleanup, err := prepareManifestTypes(t.Context(), manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	apps, err := buildAndPush(t.Context(), prepared, &imageUpload{manifest: prepared, grant: grant}, options)
	if err != nil {
		t.Fatalf("%v\n%s", err, &diagnostics)
	}
	for _, app := range apps {
		imageOptions := options
		imageOptions.metadataGrant = grant
		_, metadata, err := resolveTypeMetadata(t.Context(), app.Image, imageOptions)
		if err != nil || metadata == nil || metadata.Schema == nil {
			t.Fatalf("published metadata %s: %v, %v", app.Name, metadata, err)
		}
		if app.Name == "checkout" {
			if len(metadata.Requires) != 1 {
				t.Fatal("caller expectations not published")
			}
			repository, err := contractRepository(app.Image, imageOptions)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := typegen.FetchDocument(t.Context(), repository.Blobs(), metadata.Requires["orders"])
			if err != nil || expected.Identity.Name != "io.github.example/orders" || len(expected.Schema.Tools) != 1 {
				t.Fatalf("consumer dependency blob: %+v %v", expected, err)
			}
		}
	}
	for path, original := range originalFiles {
		data, _ := os.ReadFile(path)
		if !bytes.Equal(data, original) {
			t.Fatal("modified original package scripts or lockfile: " + path)
		}
	}
	checkout := prepared.Apps[slices.IndexFunc(prepared.Apps, func(app manifestApp) bool { return app.Name == "checkout" })]
	before := editor(checkout, "index.d.ts")
	workspace.accept(prepared)
	providerPath := filepath.Join(root, "orders/server.ts")
	if err := os.WriteFile(providerPath, []byte(strings.Replace(provider, "id:z.string()", "id:z.number()", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, cleanupChanged, err := prepareManifestTypes(t.Context(), manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupChanged()
	index := slices.IndexFunc(changed.Apps, func(app manifestApp) bool { return app.Name == "checkout" })
	if !workspace.affected(t.Context(), changed.Apps[index], changed) {
		t.Fatal("signature edit did not include caller")
	}
	if editor(changed.Apps[index], "index.d.ts") == before {
		t.Fatal("editor declarations did not update")
	}
	diagnostics.Reset()
	_, err = buildAndPush(t.Context(), manifestSpec{Apps: []manifestApp{changed.Apps[index]}}, &imageUpload{manifest: changed, grant: grant}, options)
	if err == nil || !strings.Contains(diagnostics.String(), "error TS2322") {
		t.Fatalf("expected incompatible argument diagnostic: %v\n%s", err, diagnostics.String())
	}
	if err := currentTypeSources(prepared); err == nil || !strings.Contains(err.Error(), "source changed during preparation") {
		t.Fatalf("expected superseded source rejection: %v", err)
	}
	if err := os.WriteFile(providerPath, []byte(provider), 0o644); err != nil {
		t.Fatal(err)
	}
	// Dev still rebuilds the generated clients, but a caller's type errors do not block it.
	options.check = false
	sourceFiles(t, filepath.Join(root, "checkout"), map[string]string{
		"server.ts": strings.Replace(caller, "get_order({id})", "get_order({id:42})", 1),
	})
	fixed, cleanupFixed, err := prepareManifestTypes(t.Context(), manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupFixed()
	if workspace.affected(t.Context(), fixed.Apps[index], fixed) {
		t.Fatal("handler-only edits changed the dependency signature")
	}
	if _, err := buildAndPush(t.Context(), manifestSpec{Apps: []manifestApp{fixed.Apps[index]}},
		&imageUpload{manifest: fixed, grant: grant}, options); err != nil {
		t.Fatalf("dev caller with a type error did not build: %v", err)
	}
	unreadable := strings.Replace(provider, `import {z} from "zod";`,
		`import {z} from "zod"; import {readFileSync} from "node:fs"; readFileSync("schema.json");`, 1)
	if err := os.WriteFile(providerPath, []byte(unreadable), 0o644); err != nil {
		t.Fatal(err)
	}
	// A dependency whose contract cannot be extracted fails preparation instead of becoming untyped.
	if _, _, err := prepareManifestTypes(t.Context(), manifest, options); err == nil || !strings.Contains(err.Error(), "contract evaluation has no filesystem") {
		t.Fatalf("unsupported extraction did not fail preparation: %v", err)
	}
	options.check = true
	providerRoot := filepath.Join(root, "orders")
	var rewritten map[string]any
	if err := json.Unmarshal(originalFiles[filepath.Join(providerRoot, "package.json")], &rewritten); err != nil {
		t.Fatal(err)
	}
	rewritten["scripts"] = map[string]string{"build": "node rewrite.mjs"}
	if err := writeTypeJSON(filepath.Join(providerRoot, "package.json"), rewritten); err != nil {
		t.Fatal(err)
	}
	sourceFiles(t, providerRoot, map[string]string{
		"server.ts": provider,
		"rewrite.mjs": `import {readFileSync,writeFileSync} from "node:fs";
writeFileSync("server.ts",readFileSync("server.ts","utf8").replace("id:z.string()","id:z.number()"));`,
	})
	mutated, cleanupMutated, err := prepareManifestTypes(t.Context(), manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupMutated()
	providerIndex := slices.IndexFunc(mutated.Apps, func(app manifestApp) bool { return app.Name == "orders" })
	diagnostics.Reset()
	_, err = buildAndPush(t.Context(), manifestSpec{Apps: []manifestApp{mutated.Apps[providerIndex]}}, &imageUpload{manifest: mutated, grant: grant}, options)
	if err == nil || !strings.Contains(diagnostics.String(), "refusing to publish stale types") {
		t.Fatalf("expected build-time contract mutation rejection: %v\n%s", err, diagnostics.String())
	}
}

func TestDockerPythonTypedBuild(t *testing.T) {
	root := t.TempDir()
	provider := `from mcp.server import MCPServer
from pydantic import BaseModel
server = MCPServer("orders")
class Order(BaseModel):
    total: float
@server.tool()
async def read(id: str) -> Order:
    return Order(total=5)
`
	caller := `from mcp.server import MCPServer
from holon.apps import apps
server = MCPServer("checkout")
@server.tool()
async def checkout(id: str) -> float:
    order = await apps.orders.tools.read(id=id)
    return order["total"]
`
	manifest := manifestSpec{}
	for _, name := range []string{"orders", "checkout"} {
		code := provider
		descriptor := &serverDescriptor{Name: "example.org/" + name, Version: "1"}
		app := manifestApp{Name: name, Path: filepath.Join(root, name), resolved: resolvedApp{App: protocol.App{Name: name}, descriptor: descriptor}}
		if name == "checkout" {
			code = caller
			descriptor.Requires = map[string]serverReference{"orders": {Name: "example.org/orders", Version: "1"}}
			app.resolved.Requires = []string{"orders"}
		}
		// Pin the merged keyword-call runtime until it is released on PyPI.
		sourceFiles(t, app.Path, map[string]string{
			"server.py": code,
			"requirements.txt": "holon-sh @ https://github.com/holon-sh/sdk-py/archive/a27a936f197af164a4c92c593916a64c74e60ad9.tar.gz\n" +
				"mcp==2.3.0\n",
			"package.json": `{"type":"module"}`,
			"railpack.json": `{"provider":"python","steps":{"build":{"commands":[` +
				`"python -c 'from holon.apps import apps; from holon.binding import set_default_binding; assert apps.orders and set_default_binding'",` +
				`"test ! -e .holon-types/holon-sh"]}}}`,
		})
		manifest.Apps = append(manifest.Apps, app)
	}
	manifest = typedManifest(t, manifest.Apps...)
	_, grant, options := sourceRegistry(t)
	grant.Repositories = map[string]string{"orders": "apps/orders", "checkout": "apps/checkout"}
	options.architectures = []string{"amd64", "arm64"}
	options.Env = append(options.Env, "DOCKER_CONTEXT="+os.Getenv("DOCKER_CONTEXT"))
	for _, test := range []struct{ valid, check bool }{
		{valid: true, check: true},
		{check: true},
		{},
	} {
		valid := test.valid
		options.check = test.check
		if !valid {
			sourceFiles(t, filepath.Join(root, "checkout"), map[string]string{
				"server.py": strings.Replace(caller, `read(id=id)`, `read(id=42)`, 1),
			})
		}
		prepared, cleanup, err := prepareManifestTypes(t.Context(), manifest, options)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		selected := manifestSpec{Apps: []manifestApp{prepared.Apps[1]}}
		var progress strings.Builder
		options.Stderr = &progress
		apps, err := buildAndPush(t.Context(), selected, &imageUpload{manifest: prepared, grant: grant}, options)
		if !valid && test.check {
			if err == nil || !strings.Contains(strings.ToLower(progress.String()), "incompatible type") {
				t.Fatalf("expected incompatible argument diagnostic: %v\n%s", err, progress.String())
			}
			steps := regexp.MustCompile(`(?m)^(#[0-9]+) .*/opt/holon-check/bin/pip install .*`).FindAllStringSubmatch(progress.String(), -1)
			if !slices.ContainsFunc(steps, func(step []string) bool { return strings.Contains(progress.String(), step[1]+" CACHED\n") }) {
				t.Fatalf("source edit reinstalled the type checker: %v\n%s", err, progress.String())
			}
			continue
		}
		if err != nil {
			t.Fatalf("build Python caller: %v\n%s", err, progress.String())
		}
		options.metadataGrant = grant
		_, metadata, err := resolveTypeMetadata(t.Context(), apps[0].Image, options)
		if err != nil || metadata == nil || metadata.Schema == nil || len(metadata.Requires) != 1 {
			t.Fatalf("Python build lost contract metadata: %v %v", metadata, err)
		}
	}
}
