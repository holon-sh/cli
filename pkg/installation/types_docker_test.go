//go:build docker

package installation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/holon-sh/cli/pkg/appartifact"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/typegen"

	"github.com/holon-sh/protocol"
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
  const server=new McpServer({name:"io.github.example/orders",version:"1.0.0",title:"Orders",description:"Orders by id"});
  server.registerTool("get_order", {inputSchema:z.object({id:z.string()}),outputSchema:z.object({total:z.number()})},
    ({id}) => ({content:[{type:"text",text:String(id)}],structuredContent:{total:5}}));
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
		app := manifestApp{Name: name, Path: path, resolved: resolvedApp{App: protocol.App{Name: name, Port: 3000}}}
		if name == "checkout" {
			app.resolved.descriptor = &mcpregistry.Descriptor{Name: "io.github.example/checkout", Version: "1.0.0", Requires: map[string]mcpregistry.Requirement{
				"orders": {RegistryRef: protocol.RegistryRef{Name: "io.github.example/orders", Version: "1.0.0", Registry: "https://private-registry.example"}},
			}}
			app.resolved.Requires = []string{"orders"}
		}
		manifest.Apps = append(manifest.Apps, app)
	}
	manifest = typedManifest(t, manifest.Apps...)
	grant, options := sourceRegistry(t)
	options.catalogTag = appartifact.DevelopmentTag
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
		t.Fatalf("%v\n%s", err, &diagnostics)
	}
	defer cleanup()
	apps, err := buildAndPush(t.Context(), prepared, &imageUpload{grant: grant}, options)
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
		repository, err := contractRepository(app.Image, imageOptions)
		if err != nil {
			t.Fatal(err)
		}
		if app.Name == "orders" {
			tagged, err := repository.Resolve(t.Context(), appartifact.DevelopmentTag)
			if err != nil {
				t.Fatal(err)
			}
			var build, platforms ocispec.Index
			var image ocispec.Manifest
			var config ocispec.Image
			publishedJSON(t, repository, tagged, &build)
			publishedJSON(t, repository, build.Manifests[0], &platforms)
			publishedJSON(t, repository, platforms.Manifests[0], &image)
			publishedJSON(t, repository, image.Config, &config)
			if labels := config.Config.Labels; labels["io.modelcontextprotocol.server.name"] != "io.github.example/orders" {
				t.Fatalf("code-only server labels: %v", labels)
			}
		}
		if app.Name == "checkout" {
			expected, err := typegen.FetchDocument(t.Context(), repository.Blobs(), metadata.Requires["orders"])
			if err != nil || expected.Identity.Name != "io.github.example/orders" {
				t.Fatalf("consumer dependency blob: %+v %v", expected, err)
			}
		}
	}
	checkout := prepared.Apps[slices.IndexFunc(prepared.Apps, func(app manifestApp) bool { return app.Name == "checkout" })]
	before := editor(checkout, "index.d.ts")
	workspace.accept(prepared)
	sourceFiles(t, root, map[string]string{"orders/server.ts": strings.Replace(provider, "id:z.string()", "id:z.number()", 1)})
	if _, _, err := prepareManifestTypes(t.Context(), manifest, options); err == nil ||
		!strings.Contains(err.Error(), "checkout does not type-check") || !strings.Contains(err.Error(), "error TS2322") {
		t.Fatalf("expected incompatible argument diagnostic: %v", err)
	}
	if editor(checkout, "index.d.ts") == before {
		t.Fatal("editor declarations did not update")
	}
	sourceFiles(t, root, map[string]string{"checkout/server.ts": strings.Replace(caller, "get_order({id})", "get_order({id:Number(id)})", 1)})
	changed, cleanupChanged, err := prepareManifestTypes(t.Context(), manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupChanged()
	index := slices.IndexFunc(changed.Apps, func(app manifestApp) bool { return app.Name == "checkout" })
	if !workspace.affected(t.Context(), changed.Apps[index], changed) {
		t.Fatal("signature edit did not include caller")
	}
	sourceFiles(t, root, map[string]string{"orders/server.ts": strings.Replace(provider, "total:5", "total:6", 1), "checkout/server.ts": caller})
	fixed, cleanupFixed, err := prepareManifestTypes(t.Context(), manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupFixed()
	if workspace.affected(t.Context(), fixed.Apps[index], fixed) {
		t.Fatal("handler-only edits changed the dependency signature")
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
		descriptor := &mcpregistry.Descriptor{Name: "example.org/" + name, Version: "1"}
		app := manifestApp{Name: name, Path: filepath.Join(root, name), resolved: resolvedApp{App: protocol.App{Name: name}, descriptor: descriptor}}
		if name == "checkout" {
			code = caller
			descriptor.Requires = map[string]mcpregistry.Requirement{"orders": {RegistryRef: protocol.RegistryRef{Name: "example.org/orders", Version: "1"}}}
			app.resolved.Requires = []string{"orders"}
		}
		sourceFiles(t, app.Path, map[string]string{
			"server.py": code,
			"requirements.txt": "holon-sh @ https://github.com/holon-sh/sdk-py/archive/a27a936f197af164a4c92c593916a64c74e60ad9.tar.gz\n" +
				"mcp==2.3.0\n",
			"railpack.json": `{"provider":"python","steps":{"build":{"commands":[` +
				`"python -c 'from holon.apps import apps; assert apps.orders'"]}}}`,
		})
		manifest.Apps = append(manifest.Apps, app)
	}
	manifest = typedManifest(t, manifest.Apps...)
	grant, options := sourceRegistry(t)
	grant.Repositories = map[string]string{"orders": "apps/orders", "checkout": "apps/checkout"}
	options.architectures = []string{runtime.GOARCH}
	options.Env = append(options.Env, "DOCKER_CONTEXT="+os.Getenv("DOCKER_CONTEXT"))
	for _, valid := range []bool{true, false} {
		if !valid {
			sourceFiles(t, filepath.Join(root, "checkout"), map[string]string{
				"server.py": strings.Replace(caller, `read(id=id)`, `read(id=42)`, 1),
			})
		}
		var progress strings.Builder
		options.Stderr = &progress
		prepared, cleanup, err := prepareManifestTypes(t.Context(), manifest, options)
		if !valid {
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "incompatible type") {
				t.Fatalf("expected incompatible argument diagnostic: %v\n%s", err, progress.String())
			}
			if strings.Contains(progress.String(), "Collecting mypy") || !strings.Contains(progress.String(), checkerInstall) {
				t.Fatalf("source edit reinstalled the type checker: %v\n%s", err, progress.String())
			}
			continue
		}
		if err != nil {
			t.Fatalf("%v\n%s", err, progress.String())
		}
		defer cleanup()
		selected := manifestSpec{Apps: []manifestApp{prepared.Apps[1]}}
		if _, err := buildAndPush(t.Context(), selected, &imageUpload{grant: grant}, options); err != nil {
			t.Fatalf("build Python caller: %v\n%s", err, progress.String())
		}
	}
}
