// holon-generated-apps-v1 app=checkout
import type { Outputs as Resources } from "./resources.js";
import type { AuthProvider, CallOptions, CallMetadata, ToolResult, ToolClient } from "../protocol.js";
export { ToolCallError } from "../protocol.js";
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
  "schemas": {
    "-": { [key: string]: unknown; };
    "123": { [key: string]: unknown; };
    "CallTool": { [key: string]: unknown; };
    "call_tool": { "a"?: number; "b": string; };
    "class": { "opt"?: { "n"?: number; }; };
    "get-order": { "id": string; };
    "get_order": { "id": string; };
    "kitchen-sink": { "1starts"?: string; "Dash-Key"?: string; "all"?: ({ "a": string; [key: string]: unknown; } & { "b"?: number; [key: string]: unknown; }); "allConflict"?: ({ "a"?: string; [key: string]: unknown; } & { "a"?: number; [key: string]: unknown; }); "allNonObject"?: (string & { [key: string]: unknown; }); "annotated"?: string; "arrayRef"?: string; "closedEmpty"?: {  }; "constNum"?: 1e+21; "constObj"?: unknown; "constString"?: "it's \"quoted\"\n"; "cycle"?: { "next"?: unknown; "value": number; }; "dash-key"?: string; "emptyAll"?: unknown; "emptyAny"?: never; "falseSchema"?: never; "implicitTuple"?: ["x"?, ...unknown[]]; "literals": ("a" | 1 | 2.5 | -3 | true | null | unknown | unknown | 1.0 | 1e+21 | -0.0); "mapOf"?: { [key: string]: string; }; "multi"?: (string | null | Array<number> | unknown); "nested"?: Array<Array<{ "deep"?: null; }>>; "noType": { "p": boolean; [key: string]: unknown; }; "numericType"?: { "p"?: boolean; [key: string]: unknown; }; "odd key with spaces"?: string; "oneOf"?: (string | number); "openObj"?: { "x"?: string; [key: string]: unknown; }; "openTuple"?: [boolean?, ...Array<number>]; "plainArray"?: Array<unknown>; "pointerEscape"?: number; "rootRef"?: { "1starts"?: string; "Dash-Key"?: string; "all"?: ({ "a": string; [key: string]: unknown; } & { "b"?: number; [key: string]: unknown; }); "allConflict"?: ({ "a"?: string; [key: string]: unknown; } & { "a"?: number; [key: string]: unknown; }); "allNonObject"?: (string & { [key: string]: unknown; }); "annotated"?: string; "arrayRef"?: string; "closedEmpty"?: {  }; "constNum"?: 1e+21; "constObj"?: unknown; "constString"?: "it's \"quoted\"\n"; "cycle"?: { "next"?: unknown; "value": number; }; "dash-key"?: string; "emptyAll"?: unknown; "emptyAny"?: never; "falseSchema"?: never; "implicitTuple"?: ["x"?, ...unknown[]]; "literals": ("a" | 1 | 2.5 | -3 | true | null | unknown | unknown | 1.0 | 1e+21 | -0.0); "mapOf"?: { [key: string]: string; }; "multi"?: (string | null | Array<number> | unknown); "nested"?: Array<Array<{ "deep"?: null; }>>; "noType": { "p": boolean; [key: string]: unknown; }; "numericType"?: { "p"?: boolean; [key: string]: unknown; }; "odd key with spaces"?: string; "oneOf"?: (string | number); "openObj"?: { "x"?: string; [key: string]: unknown; }; "openTuple"?: [boolean?, ...Array<number>]; "plainArray"?: Array<unknown>; "pointerEscape"?: number; "rootRef"?: unknown; "trueObj"?: { "x"?: string; [key: string]: unknown; }; "trueSchema"?: unknown; "tuple": [string, number?]; "unknownKeyword"?: unknown; }; "trueObj"?: { "x"?: string; [key: string]: unknown; }; "trueSchema"?: unknown; "tuple": [string, number?]; "unknownKeyword"?: unknown; };
    "weird-keys": { "": null; "-"?: boolean; "x\"quote"?: string; "ünïcode"?: number; };
    "émoji🎉": { "é"?: string; [key: string]: unknown; };
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
  "schemas": {
    "-": unknown;
    "123": unknown;
    "CallTool": unknown;
    "call_tool": { "x": boolean; };
    "class": Array<unknown>;
    "get-order": unknown;
    "get_order": unknown;
    "kitchen-sink": unknown;
    "weird-keys": { "a.b"?: string; "nl\nname"?: string; "tab\tname": string; };
    "émoji🎉": string;
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
  "schemas": {
    "-": {  };
    "123": {  };
    "CallTool": {  };
    "call_tool": {  };
    "class": {  };
    "get-order": {  };
    "get_order": {  };
    "kitchen-sink": { "9lives": string; "Legacy": number; "Node": { "next"?: { "next"?: unknown; "value": number; }; "value": number; }; "_private": boolean; "a/b~c": number; "for": string; "list": [string?, ...unknown[]]; };
    "weird-keys": {  };
    "émoji🎉": {  };
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
  "schemas": {
    "-": {  };
    "123": {  };
    "CallTool": {  };
    "call_tool": {  };
    "class": {  };
    "get-order": {  };
    "get_order": {  };
    "kitchen-sink": {  };
    "weird-keys": {  };
    "émoji🎉": {  };
  };
  "with-hyphen": {
    "get_order": {  };
    "then": {  };
  };
}

export type Apps = { readonly [A in keyof Inputs]: ToolClient<Inputs[A], Outputs[A], Resources[A]> };
export declare const apps: Apps;
export interface EndpointBinding { endpoint: URL | string; authProvider?: AuthProvider; fetch?: (input: never, init?: never) => Promise<unknown>; installationUID?: string; scoped?: boolean }
export interface CreateOptions { validator?: (schema: object) => (value: unknown) => boolean }
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding }, options?: CreateOptions): Pick<Apps,K>;
