// holon-generated-apps-v1 app=chéck"out
type AuthProvider = { token(): Promise<string> };
type ToolClient<I, O, R> = { tools: { [K in keyof I]: (input: I[K]) => Promise<K extends keyof O ? O[K] : unknown> }; resources: { [U in keyof R]: { read(): Promise<R[U]> } }; callTool<K extends keyof I & string>(request: { name: K; arguments: I[K] }): Promise<unknown> };
export type Resources = import("./resources.js").Outputs;
export type * from "./resources.js";
export type OrdersInputId = { "id": string; };
export type OrdersInputX1startsDashKeyAllEtc = { "1starts"?: string; "Dash-Key"?: string; "all"?: ({ "a": string; } & { "b"?: number; }); "allConflict"?: ({ "a"?: string; } & { "a"?: number; }); "allNonObject"?: (string & { [key: string]: unknown; }); "annotated"?: string; "arrayRef"?: string; "closedEmpty"?: {  }; "constNum"?: 1e+21; "constObj"?: unknown; "constString"?: "it's \"quoted\"\n"; "cycle"?: OrdersInputNode; "dash-key"?: string; "emptyAll"?: unknown; "emptyAny"?: never; "falseSchema"?: never; "implicitTuple"?: ["x"?, ...unknown[]]; "literals": ("a" | 1 | 2.5 | -3 | true | null | unknown | unknown | 1.0 | 1e+21 | -0.0); "mapOf"?: { [key: string]: string; }; "multi"?: (string | null | Array<number> | unknown); "nested"?: Array<Array<{ "deep"?: null; }>>; "noType": { "p": boolean; }; "nodes"?: OrdersInputNodes; "numericType"?: { "p"?: boolean; }; "odd key with spaces"?: string; "oneOf"?: (string | number); "openObj"?: { "x"?: string; [key: string]: unknown; }; "openTuple"?: [boolean?, ...Array<number>]; "plainArray"?: Array<unknown>; "pointerEscape"?: OrdersInputABC; "rootRef"?: OrdersInputX1startsDashKeyAllEtc; "trueObj"?: { "x"?: string; [key: string]: unknown; }; "trueSchema"?: unknown; "tuple": [string, number?]; "unknownKeyword"?: unknown; };
export type OrdersInputNode = { "next"?: OrdersInputNode; "value": number; };
export type OrdersInputNodes = Array<OrdersInputNode>;
export type OrdersInputABC = number;
export type OrdersInputX9lives = string;
export type OrdersInputLegacy = number;
export type OrdersInputPrivate = boolean;
export type OrdersInputFor = string;
export type OrdersInputList = [string?, ...unknown[]];
export interface Inputs {
  "123": {
  };
  "_private": {
  };
  "for": {
    "x": { [key: string]: unknown; };
  };
  "orders": {
    "-": { [key: string]: unknown; };
    "123": { [key: string]: unknown; };
    "CallTool": { [key: string]: unknown; };
    "_hidden": { [key: string]: unknown; };
    "call_tool": { "a"?: number; "b": string; };
    "class": { "opt"?: { "n"?: number; }; };
    "get-order": OrdersInputId;
    "get_order": OrdersInputId;
    "kitchen-sink": OrdersInputX1startsDashKeyAllEtc;
    "order.list": { [key: string]: unknown; };
    "order/list": { [key: string]: unknown; };
    "runtime.run": { "code": string; };
    "weird-keys": { "": null; "-"?: boolean; "x\"quote"?: string; "ünïcode"?: number; };
    "émoji🎉": { "é"?: string; };
  };
  "orders!": {
    "kitchen-sink": { "id": string; };
  };
  "with-hyphen": {
    "y": { [key: string]: unknown; };
  };
  "ünï": {
    "z": { [key: string]: unknown; };
  };
}
export interface Outputs {
  "123": {
  };
  "_private": {
  };
  "for": {
  };
  "orders": {
    "call_tool": { "x": boolean; };
    "class": Array<unknown>;
    "runtime.run": { "stdout": string; };
    "weird-keys": { "a.b"?: string; "nl\nname"?: string; "tab\tname": string; };
    "émoji🎉": string;
  };
  "orders!": {
    "kitchen-sink": boolean;
  };
  "with-hyphen": {
  };
  "ünï": {
  };
}
export interface InputTypes {
  "123": {
  };
  "_private": {
  };
  "for": {
    "x": {  };
  };
  "orders": {
    "-": {  };
    "123": {  };
    "CallTool": {  };
    "_hidden": {  };
    "call_tool": {  };
    "class": {  };
    "get-order": {  };
    "get_order": {  };
    "kitchen-sink": { "9lives": OrdersInputX9lives; "Legacy": OrdersInputLegacy; "Node": OrdersInputNode; "Nodes": OrdersInputNodes; "_private": OrdersInputPrivate; "a/b~c": OrdersInputABC; "for": OrdersInputFor; "list": OrdersInputList; };
    "order.list": {  };
    "order/list": {  };
    "runtime.run": {  };
    "weird-keys": {  };
    "émoji🎉": {  };
  };
  "orders!": {
    "kitchen-sink": {  };
  };
  "with-hyphen": {
    "y": {  };
  };
  "ünï": {
    "z": {  };
  };
}
export interface OutputTypes {
  "123": {
  };
  "_private": {
  };
  "for": {
  };
  "orders": {
    "call_tool": {  };
    "class": {  };
    "runtime.run": {  };
    "weird-keys": {  };
    "émoji🎉": {  };
  };
  "orders!": {
    "kitchen-sink": {  };
  };
  "with-hyphen": {
  };
  "ünï": {
  };
}

export type Apps = { readonly [A in keyof Inputs]: ToolClient<Inputs[A], Outputs[A], Resources[A]> };
export interface EndpointBinding { endpoint: URL | string; authProvider?: AuthProvider; fetch?: (input: never, init?: never) => Promise<unknown>; installationUID?: string; scoped?: boolean }
export interface CreateOptions { validator?: (schema: object) => (value: unknown) => boolean }
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding }, options?: CreateOptions): Pick<Apps,K>;
export interface ExposeOptions { signal?: AbortSignal; execute?: (name: string, args: Record<string, unknown>) => Promise<unknown> }
export declare function exposeTools(app: Apps[keyof Apps], options?: ExposeOptions): Promise<boolean>;
export type PageResult<I> = { readonly value: I; readonly issues?: undefined } | { readonly issues: ReadonlyArray<{ readonly message: string }> };
export interface PageSchema<I = unknown> { readonly "~standard": { readonly version: 1; readonly vendor: string; readonly types?: { readonly input: unknown; readonly output: I }; readonly validate: (value: unknown) => PageResult<I> | Promise<PageResult<I>>; readonly jsonSchema: { readonly input: (options: { target: "draft-2020-12" }) => object } } }
export type PageInput<S> = S extends PageSchema<infer I> ? I : never;
export interface PageAnnotations { title?: string; readOnlyHint?: boolean; destructiveHint?: boolean; idempotentHint?: boolean; openWorldHint?: boolean }
export interface PageTool<S extends PageSchema = PageSchema> { description: string; inputSchema: S; annotations?: PageAnnotations; execute: (args: PageInput<S>) => unknown }
export interface PageHost { registerTool(name: string, config: { description: string; inputSchema: PageSchema; annotations?: PageAnnotations }, callback: (args: any, extra: any) => any): { remove(): void } }
export declare function registerPageTools<S extends { [name: string]: PageSchema }>(tools: { [K in keyof S]: PageTool<S[K]> }, options?: { view?: PageHost; signal?: AbortSignal }): Promise<boolean>;
import { App as HostApp } from "@modelcontextprotocol/ext-apps";
type OwnAppKey = "orders";
export declare class App extends HostApp { readonly tools: Apps[OwnAppKey]["tools"]; readonly resources: Apps[OwnAppKey]["resources"]; readonly callTool: Apps[OwnAppKey]["callTool"] }
