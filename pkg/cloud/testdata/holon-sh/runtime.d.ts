import type { AuthProvider, CallOptions, ToolClient } from "./protocol.js";
export { ToolCallError } from "./protocol.js";
export type { AuthProvider, CallOptions, CallMetadata, ToolResult, ToolClient } from "./protocol.js";

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
}
/** Alias for an endpoint binding. */
export type EndpointBinding = Binding;
/** Calling application identity and its declared dependencies. */
export interface Identity { name: string; version: string; requires?: Record<string, { name?: string; version?: string }> }
/** Generation-time tool and JSON resource schemas. A null contract permits unvalidated callTool requests. */
export interface Contract {
  tools: Array<{ name: string; inputSchema: Record<string, unknown>; outputSchema?: Record<string, unknown> }>;
  resources?: Array<{ uri: string; outputSchema: Record<string, unknown> }>;
}
/** Untyped client returned by the low-level runtime. Generated clients specialize its inputs, outputs, and resources. */
export type RuntimeClient = ToolClient<Record<string, Record<string, unknown>>, Record<string, unknown>, Record<string, unknown>>;
/** The subset of an MCP client a session must provide. */
export interface ToolSession {
  callTool(request: { name: string; arguments?: Record<string, unknown> }, options?: Record<string, unknown>): Promise<any>;
  readResource(request: { uri: string }, options?: Record<string, unknown>): Promise<any>;
  listTools(params?: undefined, options?: Record<string, unknown>): Promise<any>;
}
/** Optional validation, binding resolution, and host-session hooks. */
export interface CreateOptions<B = Binding> {
  /** JSON Schema validator factory. The default uses Ajv. */
  validator?: (schema: object) => (value: unknown) => boolean;
  /** Binding of an app created without explicit bindings; consulted on every call. */
  resolve?: (app: string) => Binding | undefined;
  /** Session for a binding the runtime does not connect to itself (anything without an endpoint). */
  session?: (app: string, binding: B) => ToolSession | undefined;
}
/** Construct runtime clients from identity and contracts. Connections open lazily. Explicit and resolved endpoint bindings reuse one session unless scoped is true; <NAME>_MCP_URL bindings use one session per call. Close persistent clients on shutdown. Most callers use the generated package root. */
export declare function createApps<B = Binding>(identity: Identity, contracts: Record<string, Contract | null>, bindings?: Record<string, B>, options?: CreateOptions<B>): Readonly<Record<string, RuntimeClient>>;
/** Construct only the explicitly bound dependencies. Unknown names throw; an empty binding map selects none. Used by generated createApps factories. */
export declare function bindApps<B = Binding>(identity: Identity, contracts: Record<string, Contract | null>, bindings: Record<string, B>, options?: CreateOptions<B>): Readonly<Record<string, RuntimeClient>>;
/** For hosts: register a process-wide resolver for clients created without explicit bindings. Explicit bindings take precedence; return undefined to fall back to the <NAME>_MCP_URL environment variable. Register during startup. */
export declare function setDefaultBinding(resolve: (app: string) => Binding | undefined): void;
