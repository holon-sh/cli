import type { Outputs as Resources } from "./resources.js";
type AuthProvider = { token(): Promise<string> };
type ToolClient<I, O, R> = { tools: { [K in keyof I]: (input: I[K]) => Promise<K extends keyof O ? O[K] : unknown> }; resources: { [U in keyof R]: { read(): Promise<R[U]> } }; callTool<K extends keyof I & string>(request: { name: K; arguments: I[K] }): Promise<unknown> };
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

export type Apps = { readonly [A in keyof Inputs]: ToolClient<Inputs[A], Outputs[A], Resources[A]> } & { readonly "payments": UntypedClient; };
export type UntypedClient = Omit<ToolClient<Record<string, Record<string, unknown>>, Record<never, never>, Record<never, never>>, "tools"> & { readonly tools: Record<never, never> };
export declare const apps: Apps;
export interface EndpointBinding { endpoint: URL | string; authProvider?: AuthProvider; fetch?: (input: never, init?: never) => Promise<unknown>; installationUID?: string; scoped?: boolean }
export interface HostBinding { host: { callServerTool(params: { name: string; arguments?: Record<string, unknown> }, options?: { signal?: AbortSignal }): Promise<unknown>; readServerResource(params: { uri: string }, options?: { signal?: AbortSignal }): Promise<unknown> } }
export interface CreateOptions { validator?: (schema: object) => (value: unknown) => boolean }
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding | HostBinding }, options?: CreateOptions): Pick<Apps,K>;
