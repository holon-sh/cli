// holon-generated-apps-v1 app=checkout
import type { AuthProvider, CallOptions, CallMetadata, ToolResult, ToolClient } from "../protocol.js";
export { ToolCallError } from "../protocol.js";
export type Resources = import("./resources.js").Outputs;
export type * from "./resources.js";
export type MysqlInputLabeledColumn = { "column": string; "label": string; };
export type MysqlInputDirection = ("asc" | "desc");
export type MysqlOutputRoot = { "columns": Array<MysqlOutputLabeledColumn>; };
export type MysqlOutputLabeledColumn = { "value": number; };
export interface Inputs {
  "empty": {
  };
  "mysql": {
    "select": { "columns"?: (Array<string> | Array<MysqlInputLabeledColumn> | null); "direction"?: MysqlInputDirection; "table": string; };
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
    "select": MysqlOutputRoot;
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
    "select": { "Direction": MysqlInputDirection; "LabeledColumn": MysqlInputLabeledColumn; };
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
    "select": { "LabeledColumn": MysqlOutputLabeledColumn; "Root": MysqlOutputRoot; };
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
export interface EndpointBinding { endpoint: URL | string; authProvider?: AuthProvider; fetch?: (input: never, init?: never) => Promise<unknown>; installationUID?: string; scoped?: boolean }
export interface CreateOptions { validator?: (schema: object) => (value: unknown) => boolean }
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding }, options?: CreateOptions): Pick<Apps,K>;
