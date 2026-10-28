import { bindApps } from "./runtime.mjs";
export {ToolCallError, exposeTools, registerPageTools} from "./runtime.mjs";
export function createApps(bindings,options){return bindApps({"name":"a","requires":{},"version":"1"},{},bindings,options)}
