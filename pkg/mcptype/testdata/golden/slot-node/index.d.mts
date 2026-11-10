// holon-generated-apps-v1 app=checkout
import type { ToolClient, EndpointBinding, CreateOptions } from "holon-sh/runtime";
export { ToolCallError } from "holon-sh/runtime";
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
export interface Resources {
  "empty": {
  };
  "mysql": {
  };
  "orders": {
    "orders://summary": { "total": number; };
  };
  "with-hyphen": {
    "orders://summary": { "total": number; };
  };
}
export interface EventArguments {
  "orders": {
    "order.shipped": { "orderId"?: string; };
  };
  "with-hyphen": {
    "order.shipped": { "orderId"?: string; };
  };
}
export interface EventData {
  "orders": {
    "order.shipped": { "carrier"?: string; "orderId": string; };
  };
  "with-hyphen": {
    "order.shipped": { "carrier"?: string; "orderId": string; };
  };
}
export type Events = { readonly [A in keyof EventArguments]: { [K in keyof EventArguments[A]]: { arguments: EventArguments[A][K]; data: K extends keyof EventData[A] ? EventData[A][K] : unknown } } };

export type Apps = { readonly [A in keyof Inputs]: A extends keyof Events ? ToolClient<Inputs[A], Outputs[A], Resources[A], Events[A]> : ToolClient<Inputs[A], Outputs[A], Resources[A]> };
export declare const apps: Apps;
export type { EndpointBinding, CreateOptions };
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding }, options?: CreateOptions): Pick<Apps,K>;
