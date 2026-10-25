import { bindApps } from "./browser-runtime.mjs";
export {ToolCallError, exposeTools, registerPageTools} from "./browser-runtime.mjs";
export function createApps(bindings,options){return bindApps({"name":"a","requires":{},"version":"1"},{},bindings,options)}
