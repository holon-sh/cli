// holon-generated-apps-v1 app=checkout
import { bindApps, hostApps } from "../runtime.mjs";
export {ToolCallError, exposeTools, registerPageTools} from "../runtime.mjs";
const identity = {"name":"checkout","requires":{"orders":{"name":"orders","version":"1.0.0"}},"version":"1.0.0"}, contracts = {"checkout":{"tools":[{"name":"status","inputSchema":{"additionalProperties":false,"type":"object"}}]},"orders":{"tools":[{"name":"get_order","inputSchema":{"additionalProperties":false,"properties":{"count":{"type":"integer"},"orderId":{"type":"string"}},"required":["orderId"],"type":"object"},"outputSchema":{"properties":{"total":{"type":"number"}},"required":["total"],"type":"object"}},{"name":"then","inputSchema":{"additionalProperties":false,"type":"object"}}],"resources":[{"uri":"orders://summary","outputSchema":{"properties":{"total":{"type":"number"}},"required":["total"],"type":"object"}}]}};
export const { apps, view } = hostApps(identity, contracts, "checkout");
export function createApps(bindings,options){return bindApps(identity,contracts,bindings,options)}
