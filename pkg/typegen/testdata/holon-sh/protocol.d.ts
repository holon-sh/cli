// holon-sh 0.0.1-dev.6: the published declarations the generated clients are checked against.
import type { CallToolRequest, CallToolResult, ElicitRequestParams, ElicitResult, InputRequiredResult, InputResponses, ListToolsResult, RequestOptions } from "@modelcontextprotocol/client";

/** Bearer-token or OAuth provider accepted by the MCP transport. */
export type AuthProvider =
  | { token(): Promise<string | undefined>; onUnauthorized?(context: unknown): Promise<void> }
  | { tokens(): unknown; redirectUrl: unknown; clientMetadata: unknown };
/** Answers a server's question so the call that asked can complete. `params.mode` tells a form from a URL to open. */
export type ElicitHandler = (params: ElicitRequestParams) => ElicitResult | Promise<ElicitResult>;

/** MCP request options, including AbortSignal and timeout in milliseconds. Input-required responses are enabled by the runtime. */
export type CallOptions = Omit<RequestOptions, "allowInputRequired">;
/** Call options for a tool. `elicit` answers its questions in place, so the call returns the completed result. */
export type ToolCallOptions = CallOptions & { elicit?: ElicitHandler };
/** MCP request metadata and input continuation fields. Pass inputResponses and requestState when continuing an input-required result. */
export type CallMetadata = Omit<CallToolRequest["params"], "name" | "arguments"> & { inputResponses?: InputResponses; requestState?: string };
/** @internal */
type Fields<T> = { [K in keyof T as string extends K ? never : number extends K ? never : K]: T[K] };
/** Successful tool result with contract-typed structured content. */
export type CompleteResult<Output> = Omit<Fields<CallToolResult>, "structuredContent" | "isError"> & { resultType: "complete"; isError?: false; structuredContent: Output };
/** Full tool result: successful typed output, an application error, or an input-required continuation. */
export type ToolResult<Output> =
  | CompleteResult<Output>
  | (Omit<Fields<CallToolResult>, "structuredContent" | "isError"> & { resultType: "complete"; isError: true; structuredContent?: unknown })
  | InputRequiredResult;

/** Error thrown by the tools convenience methods for error or input_required results. Inspect the original result; pass elicit to answer questions in place, or continue later through callTool. */
export declare class ToolCallError extends Error {
  readonly result: Extract<ToolResult<unknown>, { isError: true } | { resultType: "input_required" }>;
  constructor(app: string, tool: string, result: ToolCallError["result"]);
}

/** Client operations shared by generated app clients. Transport, validation, and protocol errors reject the returned promises. */
export interface ToolClient<I, O, R> {
  /** Close the cached endpoint session. Scoped calls close their own session; a host-owned session is closed by its host. */
  readonly close: () => Promise<void>;
  /** Fetch the live tool catalog. */
  readonly listTools: (options?: CallOptions) => Promise<ListToolsResult>;
  /** Contracted JSON resources. read validates the first text content as JSON. watch requires an endpoint binding to a server that supports subscriptions; it reads the initial value, then listens for updates until aborted. */
  readonly resources: { readonly [U in keyof R]: {
    readonly read: (options?: CallOptions) => Promise<R[U]>;
    readonly watch: (onChange: (value: R[U]) => void | Promise<void>, options?: { signal?: AbortSignal }) => Promise<void>;
  } };
  /** Convenience methods returning structured output, or the complete result for a tool without an output schema. Error results throw ToolCallError, as do questions left unanswered. Reserved object method names remain callable through callTool. */
  readonly tools: { readonly [K in Exclude<keyof I, "then" | "toJSON" | "toString" | "valueOf" | "toLocaleString">]: (input: I[K], options?: ToolCallOptions) => Promise<K extends keyof O ? O[K] : CompleteResult<unknown>> };
  /** Return the full MCP result, preserving error results and questions left unanswered. Arguments and successful output are contract-validated. */
  readonly callTool: <K extends keyof I & string>(request: CallMetadata & { name: K; arguments: I[K] }, options?: ToolCallOptions) => Promise<ToolResult<K extends keyof O ? O[K] : unknown>>;
}
