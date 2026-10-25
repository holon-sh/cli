import { bindApps } from "./browser-runtime.mjs";
export {ToolCallError, exposeTools, registerPageTools} from "./browser-runtime.mjs";
export function createApps(bindings,options){return bindApps({"name":"a","requires":{"pay-ments":{"name":"p","version":"1"},"payments":{"name":"payments","version":"1"}},"version":"1"},{},bindings,options)}
