// holon-generated-apps-v1 app=chéck"out
import type { ToolClient, EndpointBinding, CreateOptions } from "holon-ui/runtime";
export { ToolCallError } from "holon-ui/runtime";
export type OrdersInputId = { "id": string; };
export type OrdersInputX1startsDashKeyAllEtc = { "1starts"?: string; "Dash-Key"?: string; "all"?: ({ "a": string; } & { "b"?: number; }); "allConflict"?: ({ "a"?: string; } & { "a"?: number; }); "allNonObject"?: (string & { [key: string]: unknown; }); "annotated"?: string; "arrayRef"?: string; "closedEmpty"?: {  }; "constNum"?: 1e+21; "constObj"?: unknown; "constString"?: "it's \"quoted\"\n"; "cycle"?: OrdersInputNode; "dash-key"?: string; "emptyAll"?: unknown; "emptyAny"?: never; "falseSchema"?: never; "files"?: Array<Blob>; "implicitTuple"?: ["x"?, ...unknown[]]; "literals": ("a" | 1 | 2.5 | -3 | true | null | unknown | unknown | 1.0 | 1e+21 | -0.0); "mapOf"?: { [key: string]: string; }; "multi"?: (string | null | Array<number> | unknown); "nested"?: Array<Array<{ "deep"?: null; }>>; "noType": { "p": boolean; }; "nodes"?: OrdersInputNodes; "numericType"?: { "p"?: boolean; }; "odd key with spaces"?: string; "oneOf"?: (string | number); "openObj"?: { "x"?: string; [key: string]: unknown; }; "openTuple"?: [boolean?, ...Array<number>]; "plainArray"?: Array<unknown>; "pointerEscape"?: OrdersInputABC; "rootRef"?: OrdersInputX1startsDashKeyAllEtc; "trueObj"?: { "x"?: string; [key: string]: unknown; }; "trueSchema"?: unknown; "tuple": [string, number?]; "unknownKeyword"?: unknown; };
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
export type OrdersOutputItem = { "id": string; };
export interface Resources {
  "123": {
  };
  "_private": {
  };
  "for": {
  };
  "orders": {
    "res://plain": string;
    "res://typed": { "items": Array<OrdersOutputItem>; };
    "res://é/ü": { "n"?: number; };
  };
  "orders!": {
  };
  "with-hyphen": {
  };
  "ünï": {
  };
}
export interface EventArguments {
  "123": {
  };
  "_private": {
  };
  "for": {
  };
  "orders": {
  };
  "orders!": {
  };
  "with-hyphen": {
  };
  "ünï": {
  };
}
export interface EventData {
  "123": {
  };
  "_private": {
  };
  "for": {
  };
  "orders": {
  };
  "orders!": {
  };
  "with-hyphen": {
  };
  "ünï": {
  };
}
export type Events = { readonly [A in keyof EventArguments]: { [K in keyof EventArguments[A]]: { arguments: EventArguments[A][K]; data: K extends keyof EventData[A] ? EventData[A][K] : unknown } } };

export type Apps = { readonly [A in keyof Inputs]: ToolClient<Inputs[A], Outputs[A], Resources[A], Events[A]> };
export declare const connect: import("holon-ui/runtime").Connect<Apps["orders"]>;
export type { EndpointBinding, CreateOptions };
export declare function createApps<K extends keyof Apps>(bindings: { [A in K]: EndpointBinding }, options?: CreateOptions): Pick<Apps,K>;
