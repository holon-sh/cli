package mcptype

import (
	"context"
	"fmt"

	"github.com/holon-sh/protocol"
)

// bindingTypes declares the generated factory over the runtime's own binding and option types, so a
// consumer's object literals are checked against the installed runtime rather than a copy of it.
const bindingTypes = `export type { EndpointBinding, CreateOptions };
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding }, options?: CreateOptions): Pick<Apps,K>;
`

// TypeScriptModule is the generated clients' module, beside its declarations in index.d.mts. Its .mjs
// keeps it an ES module in a CommonJS project.
const TypeScriptModule = "index.mjs"

// resourceContracts compiles resources as tools named by URI, so a resource's output is its contents.
func resourceContracts(contracts map[string]contract) map[string]contract {
	result := make(map[string]contract, len(contracts))
	for alias, source := range contracts {
		tools := make(map[string]contractTool, len(source.Resources))
		for uri, resource := range source.Resources {
			tools[uri] = contractTool{Input: Object{"type": "object", "additionalProperties": false}, Output: resource}
		}
		result[alias] = contract{Tools: tools}
	}
	return result
}

const eventTypes = "export type Events = { readonly [A in keyof EventArguments]: { [K in keyof EventArguments[A]]: { arguments: EventArguments[A][K]; data: K extends keyof EventData[A] ? EventData[A][K] : unknown } } };\n"

// runtimes are the modules each TypeScript target's clients import their runtime and its declarations from.
var runtimes = map[string]string{"node": quoted("holon-sh/runtime"), "browser": quoted("holon-ui/runtime")}

func generateTypeScriptPackage(ctx context.Context, request generateRequest, catalogs map[string]catalog) (map[string]string, error) {
	// Resources take names that tools left free, in the same declarations.
	names := map[string]bool{}
	tools := generateTypeScript(catalogs, names, [4]string{"Inputs", "Outputs", "InputTypes", "OutputTypes"})
	catalogs, err := compileCatalogs(ctx, resourceContracts(request.contracts), true)
	if err != nil {
		return nil, err
	}
	resources := generateTypeScript(catalogs, names, [4]string{1: "Resources"})
	catalogs, err = compileCatalogs(ctx, eventContracts(request.contracts), false)
	if err != nil {
		return nil, err
	}
	events := generateTypeScript(catalogs, names, [4]string{"EventArguments", "EventData"}) + eventTypes
	runtime := runtimes[request.Target]
	types := fmt.Sprintf("import type { ToolClient, EndpointBinding, CreateOptions } from %s;\nexport { ToolCallError } from %s;\n%s%s%s\nexport type Apps = { readonly [A in keyof Inputs]: ToolClient<Inputs[A], Outputs[A], Resources[A], Events[A]> };\n", runtime, runtime, tools, resources, events)
	identity, contracts := encode(request.caller()), encode(request.contracts)
	bound := fmt.Sprintf("const identity = %s, contracts = %s;\nexport function createApps(bindings,options){return bindApps(identity,contracts,bindings,options)}\n", identity, contracts)
	imports, module, declarations := "bindApps", bound, types
	if request.Target == "node" {
		imports, module, declarations = "createApps as create, bindApps", module+"export const apps = create(identity, contracts);\n", declarations+"export declare const apps: Apps;\n"
	} else if own := quoted(request.Own); request.Own != "" {
		imports += ", connect as open"
		module += fmt.Sprintf("export const connect = options => open({ identity, contracts, own: %s, endpoint: %s }, options);\n", own, quoted(protocol.AppPath(request.App)))
		declarations += fmt.Sprintf("export declare const connect: import(%s).Connect<Apps[%s]>;\n", runtime, own)
	}
	return map[string]string{
		TypeScriptModule: fmt.Sprintf("import { %s } from %s;\nexport { ToolCallError } from %s;\n%s", imports, runtime, runtime, module),
		"index.d.mts":    declarations + bindingTypes,
	}, nil
}
