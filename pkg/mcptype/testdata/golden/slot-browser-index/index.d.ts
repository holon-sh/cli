// holon-generated-apps-v1 app=checkout
import type { Outputs as Resources } from "./resources.js";
import type { AuthProvider, CallOptions, CallMetadata, ToolResult, ToolClient } from "holon-core/protocol";
export { ToolCallError } from "holon-core/protocol";
export type { Resources };
export interface Inputs {
  "empty": {
  };
  "mysql": {
    "select": { "columns"?: (Array<string> | Array<{ "column": string; "label": string; }> | null); "direction"?: ("asc" | "desc"); "table": string; };
  };
  "orders": {
    "get_order": { "count"?: number; "orderId": string; };
    "then": {  };
  };
  "with-hyphen": {
    "get_order": { "count"?: number; "orderId": string; };
    "then": {  };
  };
}
export interface Outputs {
  "empty": {
  };
  "mysql": {
    "select": { "columns": Array<{ "value": number; }>; };
  };
  "orders": {
    "get_order": { "total": number; [key: string]: unknown; };
    "then": unknown;
  };
  "with-hyphen": {
    "get_order": { "total": number; [key: string]: unknown; };
    "then": unknown;
  };
}
export interface InputTypes {
  "empty": {
  };
  "mysql": {
    "select": { "Direction": ("asc" | "desc"); "LabeledColumn": { "column": string; "label": string; }; };
  };
  "orders": {
    "get_order": {  };
    "then": {  };
  };
  "with-hyphen": {
    "get_order": {  };
    "then": {  };
  };
}
export interface OutputTypes {
  "empty": {
  };
  "mysql": {
    "select": { "LabeledColumn": { "value": number; }; "Root": { "columns": Array<{ "value": number; }>; }; };
  };
  "orders": {
    "get_order": {  };
    "then": {  };
  };
  "with-hyphen": {
    "get_order": {  };
    "then": {  };
  };
}

export type Apps = { readonly [A in keyof Inputs]: ToolClient<Inputs[A], Outputs[A], Resources[A]> };
export interface EndpointBinding { endpoint: URL | string; authProvider?: AuthProvider; fetch?: (input: never, init?: never) => Promise<unknown>; installationUID?: string; scoped?: boolean }
export interface HostBinding { host: { callServerTool(params: { name: string; arguments?: Record<string, unknown> }, options?: { signal?: AbortSignal }): Promise<unknown>; readServerResource(params: { uri: string }, options?: { signal?: AbortSignal }): Promise<unknown> } }
export interface CreateOptions { validator?: (schema: object) => (value: unknown) => boolean }
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding | HostBinding }, options?: CreateOptions): Pick<Apps,K>;
export interface ExposeOptions { signal?: AbortSignal; execute?: (name: string, args: Record<string, unknown>) => Promise<unknown> }
export declare function exposeTools(app: Apps[keyof Apps], options?: ExposeOptions): Promise<boolean>;
export type PageResult<I> = { readonly value: I; readonly issues?: undefined } | { readonly issues: ReadonlyArray<{ readonly message: string }> };
export interface PageSchema<I = unknown> { readonly "~standard": { readonly version: 1; readonly vendor: string; readonly types?: { readonly input: unknown; readonly output: I }; readonly validate: (value: unknown) => PageResult<I> | Promise<PageResult<I>>; readonly jsonSchema: { readonly input: (options: { target: "draft-2020-12" }) => object } } }
export type PageInput<S> = S extends PageSchema<infer I> ? I : never;
export interface PageAnnotations { title?: string; readOnlyHint?: boolean; destructiveHint?: boolean; idempotentHint?: boolean; openWorldHint?: boolean }
export interface PageTool<S extends PageSchema = PageSchema> { description: string; inputSchema: S; annotations?: PageAnnotations; execute: (args: PageInput<S>) => unknown }
export interface PageHost { registerTool(name: string, config: { description: string; inputSchema: PageSchema; annotations?: PageAnnotations }, callback: (args: any, extra: any) => any): { remove(): void } }
export declare function registerPageTools<S extends { [name: string]: PageSchema }>(tools: { [K in keyof S]: PageTool<S[K]> }, options?: { view?: PageHost; signal?: AbortSignal }): Promise<boolean>;
