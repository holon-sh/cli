import { createApps as create, bindApps } from "./runtime.mjs";
export { ToolCallError } from "./runtime.mjs";
export const apps = create({"name":"a","requires":{},"version":"1"}, {});
export function createApps(bindings,options){return bindApps({"name":"a","requires":{},"version":"1"},{},bindings,options)}
