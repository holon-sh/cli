package mcptype

import (
	"cmp"
	"context"
	"fmt"
)

// Generated entries fill the index.* slot of holon-sh or holon-ui.

const bindingTypes = `export interface EndpointBinding { endpoint: URL | string; authProvider?: AuthProvider; fetch?: (input: never, init?: never) => Promise<unknown>; installationUID?: string; scoped?: boolean }
export interface CreateOptions { validator?: (schema: object) => (value: unknown) => boolean }
`

const factoryTypes = `export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: %s }, options?: CreateOptions): Pick<Apps,K>;
`

const exposeTypes = `export interface ExposeOptions { signal?: AbortSignal; execute?: (name: string, args: Record<string, unknown>) => Promise<unknown> }
export declare function exposeTools(app: Apps[keyof Apps], options?: ExposeOptions): Promise<boolean>;
export type PageResult<I> = { readonly value: I; readonly issues?: undefined } | { readonly issues: ReadonlyArray<{ readonly message: string }> };
export interface PageSchema<I = unknown> { readonly "~standard": { readonly version: 1; readonly vendor: string; readonly types?: { readonly input: unknown; readonly output: I }; readonly validate: (value: unknown) => PageResult<I> | Promise<PageResult<I>>; readonly jsonSchema: { readonly input: (options: { target: "draft-2020-12" }) => object } } }
export type PageInput<S> = S extends PageSchema<infer I> ? I : never;
export interface PageAnnotations { title?: string; readOnlyHint?: boolean; destructiveHint?: boolean; idempotentHint?: boolean; openWorldHint?: boolean }
export interface PageTool<S extends PageSchema = PageSchema> { description: string; inputSchema: S; annotations?: PageAnnotations; execute: (args: PageInput<S>) => unknown }
export interface PageHost { registerTool(name: string, config: { description: string; inputSchema: PageSchema; annotations?: PageAnnotations }, callback: (args: any, extra: any) => any): { remove(): void } }
export declare function registerPageTools<S extends { [name: string]: PageSchema }>(tools: { [K in keyof S]: PageTool<S[K]> }, options?: { view?: PageHost; signal?: AbortSignal }): Promise<boolean>;
`

// resourceContracts compiles resources as tools named by URI, so Outputs[alias][uri] is the resource's contents.
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

func generateTypeScriptPackage(ctx context.Context, request generateRequest, catalogs map[string]catalog) (map[string]string, error) {
	tools := generateTypeScript(catalogs)
	catalogs, err := compileCatalogs(ctx, resourceContracts(request.contracts))
	if err != nil {
		return nil, err
	}
	resources := generateTypeScript(catalogs)
	var protocol string
	switch {
	case request.ProtocolSpecifier != "":
		specifier := quoted(request.ProtocolSpecifier)
		protocol = fmt.Sprintf("import type { AuthProvider, CallOptions, CallMetadata, ToolResult, ToolClient } from %s;\nexport { ToolCallError } from %s;", specifier, specifier)
	case request.Protocol != "":
		protocol = request.Protocol
	}
	identity, contracts := encode(request.caller()), encode(request.contracts)
	types := func(resources string) string {
		return fmt.Sprintf("import type { Outputs as Resources } from \"./%s.js\";\n%s\nexport type { Resources };\n%s\nexport type Apps = { readonly [A in keyof Inputs]: ToolClient<Inputs[A], Outputs[A], Resources[A]> };\n", resources, protocol, tools)
	}
	bound := fmt.Sprintf("export function createApps(bindings,options){return bindApps(%s,%s,bindings,options)}\n", identity, contracts)
	if request.Target == "browser" {
		runtime := quoted(cmp.Or(request.RuntimeSpecifier, "./runtime.mjs"))
		files := map[string]string{
			"index.js":       fmt.Sprintf("import { bindApps } from %s;\nexport {ToolCallError, exposeTools, registerPageTools} from %s;\n%s", runtime, runtime, bound),
			"index.d.ts":     types("resources") + bindingTypes + fmt.Sprintf(factoryTypes, "EndpointBinding") + exposeTypes,
			"resources.d.ts": resources,
		}
		if request.Own != "" {
			own := quoted(request.Own)
			files["index.js"] = fmt.Sprintf("import { bindApps, appClass } from %s;\nexport {ToolCallError, exposeTools, registerPageTools} from %s;\nconst identity = %s, contracts = %s;\nexport const App = appClass(identity, contracts, %s);\nexport function createApps(bindings,options){return bindApps(identity,contracts,bindings,options)}\n", runtime, runtime, identity, contracts, own)
			files["index.d.ts"] += fmt.Sprintf("import { App as HostApp } from \"@modelcontextprotocol/ext-apps\";\nexport declare class App extends HostApp { readonly tools: Apps[%s][\"tools\"]; readonly resources: Apps[%s][\"resources\"]; readonly callTool: Apps[%s][\"callTool\"] }\n", own, own, own)
		}
		return files, nil
	}
	runtime := quoted(cmp.Or(request.RuntimeSpecifier, "./runtime.mjs"))
	return map[string]string{
		"index.js":       fmt.Sprintf("import { createApps as create, bindApps } from %s;\nexport { ToolCallError } from %s;\nexport const apps = create(%s, %s);\n%s", runtime, runtime, identity, contracts, bound),
		"index.d.ts":     types("resources") + "export declare const apps: Apps;\n" + bindingTypes + fmt.Sprintf(factoryTypes, "EndpointBinding"),
		"resources.d.ts": resources,
	}, nil
}
