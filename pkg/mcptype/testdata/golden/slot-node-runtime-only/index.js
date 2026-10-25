import { createApps as create, bindApps } from "@holon/apps/runtime.mjs";
export { ToolCallError } from "@holon/apps/runtime.mjs";
export const apps = create({"name":"a","requires":{},"version":"1"}, {});
export function createApps(bindings,options){return bindApps({"name":"a","requires":{},"version":"1"},{},bindings,options)}
