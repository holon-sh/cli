// holon-sh with MCP Events under ext (holon-sh/sdk-ts main at 84ada1d, unreleased), from its dist: the declarations the generated clients are checked against.
import type { CacheableRequestOptions, CallToolRequest, CallToolRequestOptions, CallToolResult, InputRequiredResult, ListToolsRequest, ListToolsResult, ReadResourceRequest, ReadResourceResult } from "@modelcontextprotocol/client";
import type { AuthProvider, ToolClient } from "./protocol.js";
export { ToolCallError } from "./protocol.js";
export type { AuthProvider, CallOptions, CallMetadata, ElicitHandler, EventOccurrence, EventType, SubscribeOptions, Subscription, ToolCallOptions, ToolResult, ToolClient } from "./protocol.js";
/** Connection settings for an MCP endpoint. */
export interface Binding {
    /** Streamable HTTP MCP endpoint. */
    endpoint: URL | string;
    /** Authentication provider; use application-owned credentials. */
    authProvider?: AuthProvider;
    /** Custom fetch implementation, for example for an in-process transport. */
    fetch?: typeof fetch;
    /** Optional X-Holon-Installation-UID routing header. */
    installationUID?: string;
    /** Close the session after every call instead of keeping one connection open. */
    scoped?: boolean;
    /** Origins the server may name as file upload or download targets, e.g. "https://storage.example.com". Unset, any https origin on a globally reachable address is allowed; set, only these, which may also be http on loopback or have a non-global address. */
    fileOrigins?: string[];
}
/** Alias for an endpoint binding. */
export type EndpointBinding = Binding;
/** Calling application identity and its declared dependencies. */
export interface Identity {
    name: string;
    version: string;
    requires?: Record<string, {
        name?: string;
        version?: string;
    }>;
}
/** Generation-time tool, JSON resource and event schemas. A null contract permits unvalidated callTool requests. */
export interface Contract {
    tools: Array<{
        name: string;
        inputSchema: Record<string, unknown>;
        outputSchema?: Record<string, unknown>;
    }>;
    resources?: Array<{
        uri: string;
        outputSchema: Record<string, unknown>;
    }>;
    events?: Array<{
        name: string;
        inputSchema?: Record<string, unknown>;
        payloadSchema?: Record<string, unknown>;
    }>;
}
/** Untyped client returned by the low-level runtime. Generated clients specialize its inputs, outputs, and resources. */
export type RuntimeClient = ToolClient<Record<string, Record<string, unknown>>, Record<string, unknown>, Record<string, unknown>>;
/** The subset of an MCP client a session must provide. A tool call may also resolve to the server's input_required question. */
export interface ToolSession {
    callTool(request: CallToolRequest["params"], options?: CallToolRequestOptions): Promise<CallToolResult | InputRequiredResult>;
    readResource(request: ReadResourceRequest["params"], options?: CacheableRequestOptions): Promise<ReadResourceResult>;
    listTools(params?: ListToolsRequest["params"], options?: CacheableRequestOptions): Promise<ListToolsResult>;
}
/** Optional validation, binding resolution, and host-session hooks. */
export interface CreateOptions<B = Binding> {
    /** JSON Schema validator factory. The default uses Ajv. A check may set errors, which a failure reports. */
    validator?: (schema: object) => {
        (value: unknown): boolean;
        errors?: unknown;
    };
    /** Binding of an app created without explicit bindings; consulted on every call. */
    resolve?: (app: string) => Binding | undefined;
    /** Session for a binding the runtime does not connect to itself (anything without an endpoint). */
    session?: (app: string, binding: B) => ToolSession | undefined;
}
/** Construct only the explicitly bound dependencies. Unknown names throw; an empty binding map selects none. Used by generated createApps factories. */
export declare function bindApps<B = Binding>(identity: Identity, contracts: Record<string, Contract | null>, bindings: Record<string, B>, options?: CreateOptions<B>): Readonly<Record<string, RuntimeClient>>;
/** Construct runtime clients from identity and contracts. Connections open lazily. Explicit and resolved endpoint bindings reuse one session unless scoped is true; <NAME>_MCP_URL bindings use one session per call. Close persistent clients on shutdown. Most callers use the generated clients. */
export declare function createApps<B = Binding>(identity: Identity, contracts: Record<string, Contract | null>, bindings?: Record<string, B>, options?: CreateOptions<B>): Readonly<Record<string, RuntimeClient>>;
