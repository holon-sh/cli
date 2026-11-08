package mcptype

import (
	"cmp"
	"context"
	"fmt"
	"strings"
)

// bindingTypes declares the generated factory over the runtime's own binding and option types, so a
// consumer's object literals are checked against the installed runtime rather than a copy of it.
// The runtime's binding requires an endpoint; a session hook serves the app without dialing it.
const bindingTypes = `export type { EndpointBinding, CreateOptions };
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding }, options?: CreateOptions): Pick<Apps,K>;
`

// exposeTypes re-exports the browser runtime's page tools and narrows exposeTools to these apps.
const exposeTypes = `export type { ExposeOptions, PageResult, PageSchema, PageInput, PageAnnotations, PageTool, PageHost } from %[1]s;
export { registerPageTools } from %[1]s;
export declare function exposeTools(app: Apps[keyof Apps], options?: import(%[1]s).ExposeOptions): Promise<boolean>;
`

// resourceContracts compiles resources as tools named by URI, so a resource's output is its contents.
func resourceContracts(contracts map[string]contract) map[string]contract {
	result := make(map[string]contract, len(contracts))
	for alias, source := range contracts {
		tools := make(map[string]contractTool, len(source.Resources))
		for uri, resource := range source.Resources {
			tools[uri] = contractTool{Input: Object{"type": "object", "additionalProperties": false}, Output: resource.Output}
		}
		result[alias] = contract{Tools: tools}
	}
	return result
}

// declarationModule names the declarations beside an ES module. TypeScript pairs runtime.mjs with a
// runtime.d.mts, but the runtimes ship runtime.d.ts, which the specifier runtime.js resolves to.
func declarationModule(specifier string) string {
	if module, ok := strings.CutSuffix(specifier, ".mjs"); ok {
		return module + ".js"
	}
	return specifier
}

func generateTypeScriptPackage(ctx context.Context, request generateRequest, catalogs map[string]catalog) (map[string]string, error) {
	// Resources take names that tools left free, in the same declarations.
	names := map[string]bool{}
	tools := generateTypeScript(catalogs, names, [4]string{"Inputs", "Outputs", "InputTypes", "OutputTypes"})
	catalogs, err := compileCatalogs(ctx, resourceContracts(request.contracts))
	if err != nil {
		return nil, err
	}
	resources := generateTypeScript(catalogs, names, [4]string{1: "Resources"})
	runtime := cmp.Or(request.RuntimeSpecifier, "./runtime.mjs")
	declarations := quoted(declarationModule(runtime))
	types := fmt.Sprintf("import type { ToolClient, EndpointBinding, CreateOptions } from %s;\nexport { ToolCallError } from %s;\n%s%s\nexport type Apps = { readonly [A in keyof Inputs]: ToolClient<Inputs[A], Outputs[A], Resources[A]> };\n", declarations, declarations, tools, resources)
	identity, contracts := encode(request.caller()), encode(request.contracts)
	bound := fmt.Sprintf("const identity = %s, contracts = %s;\nexport function createApps(bindings,options){return bindApps(identity,contracts,bindings,options)}\n", identity, contracts)
	runtime = quoted(runtime)
	if request.Target == "browser" {
		files := map[string]string{
			"index.js":   fmt.Sprintf("import { bindApps, appClass } from %s;\nexport {ToolCallError, exposeTools, registerPageTools} from %s;\n%s", runtime, runtime, bound),
			"index.d.ts": types + bindingTypes + fmt.Sprintf(exposeTypes, declarations),
		}
		if request.Own != "" {
			own := quoted(request.Own)
			files["index.js"] += fmt.Sprintf("export const App = /* @__PURE__ */ appClass(identity, contracts, %s);\n", own)
			files["index.d.ts"] += fmt.Sprintf("import { App as HostApp } from \"@modelcontextprotocol/ext-apps\";\ntype OwnAppKey = %s;\nexport declare class App extends HostApp { readonly tools: Apps[OwnAppKey][\"tools\"]; readonly resources: Apps[OwnAppKey][\"resources\"]; readonly callTool: Apps[OwnAppKey][\"callTool\"] }\n", own)
		}
		return files, nil
	}
	return map[string]string{
		"index.js":   fmt.Sprintf("import { createApps as create, bindApps } from %s;\nexport { ToolCallError } from %s;\n%sexport const apps = create(identity, contracts);\n", runtime, runtime, bound),
		"index.d.ts": types + "export declare const apps: Apps;\n" + bindingTypes,
	}, nil
}
