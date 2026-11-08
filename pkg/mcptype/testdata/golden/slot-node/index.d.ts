// holon-generated-apps-v1 app=checkout
import type { CallOptions, CallMetadata, ToolResult, ToolClient } from "../protocol.js";
export { ToolCallError } from "../protocol.js";
import type { EndpointBinding, CreateOptions } from "../runtime.js";
export type Resources = import("./resources.js").Outputs;
export type * from "./resources.js";
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
    "get_order": { "total": number; };
  };
  "with-hyphen": {
    "get_order": { "total": number; };
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
  };
  "with-hyphen": {
    "get_order": {  };
  };
}

export type Apps = { readonly [A in keyof Inputs]: ToolClient<Inputs[A], Outputs[A], Resources[A]> };
export declare const apps: Apps;
export type { EndpointBinding, CreateOptions };
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding }, options?: CreateOptions): Pick<Apps,K>;
