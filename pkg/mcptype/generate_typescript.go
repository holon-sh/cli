package mcptype

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// The @holon-sh/apps-style package: index.* for Node, browser.* for browsers, unless the request names the entry.

const bindingTypes = `export interface EndpointBinding { endpoint: URL | string; authProvider?: AuthProvider; fetch?: (input: never, init?: never) => Promise<unknown>; installationUID?: string; scoped?: boolean }
export interface HostBinding { host: { callServerTool(params: { name: string; arguments?: Record<string, unknown> }, options?: { signal?: AbortSignal }): Promise<unknown>; readServerResource(params: { uri: string }, options?: { signal?: AbortSignal }): Promise<unknown> } }
export interface CreateOptions { validator?: (schema: object) => (value: unknown) => boolean }
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding | HostBinding }, options?: CreateOptions): Pick<Apps,K>;
`

// Browser bindings reach endpoints directly.
const browserBindingTypes = `export interface EndpointBinding { endpoint: URL | string; authProvider?: AuthProvider; fetch?: (input: never, init?: never) => Promise<unknown>; installationUID?: string; scoped?: boolean }
export interface CreateOptions { validator?: (schema: object) => (value: unknown) => boolean }
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding }, options?: CreateOptions): Pick<Apps,K>;
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
func resourceContracts(contracts map[string]Contract) map[string]Contract {
	result := make(map[string]Contract, len(contracts))
	for alias, contract := range contracts {
		tools := make(map[string]Tool, len(contract.Resources))
		for uri, resource := range contract.Resources {
			tools[uri] = Tool{Input: Object{"type": "object", "additionalProperties": false}, Output: resource.Output}
		}
		result[alias] = Contract{Tools: tools}
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
	case request.protocolSpecifier != nil:
		specifier := quoted(*request.protocolSpecifier)
		protocol = fmt.Sprintf("import type { AuthProvider, CallOptions, CallMetadata, ToolResult, ToolClient } from %s;\nexport { ToolCallError } from %s;", specifier, specifier)
	case request.protocol != nil:
		protocol = *request.protocol
	}
	var identity, contracts bytes.Buffer
	request.writeCaller(&identity)
	writeContracts(&contracts, request.contracts)
	untyped := untypedTypeScript(request)
	types := func(resources string) string {
		return fmt.Sprintf("import type { Outputs as Resources } from \"./%s.js\";\n%s\nexport type { Resources };\n%s\nexport type Apps = { readonly [A in keyof Inputs]: ToolClient<Inputs[A], Outputs[A], Resources[A]> }%s;\n", resources, protocol, tools, untyped)
	}
	bound := fmt.Sprintf("export function createApps(bindings,options){return bindApps(%s,%s,bindings,options)}\n", &identity, &contracts)
	runtime := quoted(valueOr(request.runtimeSpecifier, "./runtime.mjs"))
	browserRuntime := quoted(valueOr(request.browserRuntimeSpecifier, "./browser-runtime.mjs"))
	entry := "index"
	if request.target == "browser" {
		entry = "browser"
	}
	entry = valueOr(request.entry, entry)
	resourcesEntry := "resources"
	if entry != "index" {
		resourcesEntry = entry + "-resources"
	}
	var files map[string]string
	switch {
	case request.target == "browser":
		// Legacy (pre-slot) packages keep the host binding form their consumers were built against.
		bindings := browserBindingTypes
		if !request.slot() {
			bindings = bindingTypes
		}
		files = map[string]string{
			entry + ".js":   fmt.Sprintf("import { bindApps } from %s;\nexport {ToolCallError, exposeTools, registerPageTools} from %s;\n%s", browserRuntime, browserRuntime, bound),
			entry + ".d.ts": types(resourcesEntry) + bindings + exposeTypes,
		}
	default:
		files = map[string]string{
			entry + ".js":   fmt.Sprintf("import { createApps as create, bindApps } from %s;\nexport { ToolCallError } from %s;\nexport const apps = create(%s, %s);\n%s", runtime, runtime, &identity, &contracts, bound),
			entry + ".d.ts": types(resourcesEntry) + "export declare const apps: Apps;\n" + bindingTypes,
		}
	}
	files[resourcesEntry+".d.ts"] = resources
	if !request.slot() {
		files["package.json"] = packageJSON(request.packageFields)
	}
	return files, nil
}

func valueOr(value *string, fallback string) string {
	if value != nil {
		return *value
	}
	return fallback
}

// untypedTypeScript declares members for aliases without a contract: callTool and listTools by any name, no tools or resources.
func untypedTypeScript(request generateRequest) string {
	var aliases strings.Builder
	for _, alias := range request.untyped() {
		fmt.Fprintf(&aliases, " readonly %s: UntypedClient;", quoted(alias))
	}
	if aliases.Len() == 0 {
		return ""
	}
	return fmt.Sprintf(" & {%s };\nexport type UntypedClient = Omit<ToolClient<Record<string, Record<string, unknown>>, Record<never, never>, Record<never, never>>, \"tools\"> & { readonly tools: Record<never, never> }", aliases.String())
}

// packageJSON is package.json with two-space indentation and the extra fields between type and exports.
func packageJSON(fields Object) string {
	var text strings.Builder
	text.WriteString("{\n  \"name\": \"@holon-sh/apps\",\n  \"version\": \"0.0.0\",\n  \"private\": true,\n  \"type\": \"module\",\n")
	for _, name := range sortedKeys(fields) {
		fmt.Fprintf(&text, "  %s: %s,\n", quoted(name), strings.ReplaceAll(encodePretty(fields[name]), "\n", "\n  "))
	}
	text.WriteString(`  "exports": {
    ".": {
      "types": "./index.d.ts",
      "import": "./index.js"
    },
    "./browser": {
      "types": "./browser.d.ts",
      "import": "./browser.js"
    }
  }
}
`)
	return text.String()
}
