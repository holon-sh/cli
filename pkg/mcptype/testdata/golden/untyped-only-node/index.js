import { createApps as create, bindApps } from "./runtime.mjs";
export { ToolCallError } from "./runtime.mjs";
export const apps = create({"name":"a","requires":{"pay-ments":{"name":"p","version":"1"},"payments":{"name":"payments","version":"1"}},"version":"1"}, {});
export function createApps(bindings,options){return bindApps({"name":"a","requires":{"pay-ments":{"name":"p","version":"1"},"payments":{"name":"payments","version":"1"}},"version":"1"},{},bindings,options)}
