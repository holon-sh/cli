// holon-sh with MCP Events under ext (holon-sh/sdk-ts main at 84ada1d, unreleased), from its dist: the declarations the generated clients are checked against.
import type { AuthProvider as BearerAuthProvider, CallToolRequest, CallToolResult, ElicitRequestParams, ElicitResult, InputRequiredResult, InputResponses, ListToolsResult, OAuthClientProvider, RequestOptions } from "@modelcontextprotocol/client";
import type { Download } from "./ext/files.js";
/** Bearer-token or OAuth provider accepted by the MCP transport. */
export type AuthProvider = BearerAuthProvider | OAuthClientProvider;
/** Answers a server's question so the call that asked can complete. `params.mode` tells a form from a URL to open. */
export type ElicitHandler = (params: ElicitRequestParams) => ElicitResult | Promise<ElicitResult>;
/** MCP request options, including AbortSignal and timeout in milliseconds. A request without a timeout waits for its answer; progress from the server restarts a timeout. Input-required responses are enabled by the runtime. */
export type CallOptions = Omit<RequestOptions, "allowInputRequired">;
/** Call options for a tool. `elicit` answers its questions in place, so the call returns the completed result. */
export type ToolCallOptions = CallOptions & {
    elicit?: ElicitHandler;
};
/** MCP request metadata and input continuation fields. Pass inputResponses and requestState when continuing an input-required result. */
export type CallMetadata = Omit<CallToolRequest["params"], "name" | "arguments"> & {
    inputResponses?: InputResponses;
    requestState?: string;
};
type Fields<T> = {
    [K in keyof T as string extends K ? never : number extends K ? never : K]: T[K];
};
type Files = {
    /** Files the tool returned, from a server with the files extension. Their targets are removed from _meta. */
    downloads?: Download[];
};
/** Successful tool result with contract-typed structured content. */
export type CompleteResult<Output> = Omit<Fields<CallToolResult>, "structuredContent" | "isError"> & Files & {
    resultType: "complete";
    isError?: false;
    structuredContent: Output;
};
/** Full tool result: successful typed output, an application error, or an input-required continuation. */
export type ToolResult<Output> = CompleteResult<Output> | (Omit<Fields<CallToolResult>, "structuredContent" | "isError"> & Files & {
    resultType: "complete";
    isError: true;
    structuredContent?: unknown;
}) | InputRequiredResult;
/** Error thrown by the tools convenience methods for error or input_required results. A failure's message carries its text; `data` is what the server said in a form to branch on. Pass elicit to answer questions in place, or continue later through callTool. */
export declare class ToolCallError extends Error {
    readonly result: Extract<ToolResult<unknown>, {
        isError: true;
    } | {
        resultType: "input_required";
    }>;
    /** The failure's text content, its text blocks joined by newlines. Undefined for questions left unanswered. */
    readonly text?: string;
    /** The failure's structured content, or else its text parsed as JSON when that text is JSON. Its shape is the server's own. */
    readonly data?: unknown;
    constructor(app: string, tool: string, result: ToolCallError["result"]);
}
/** An event type a server offers, as events/list describes it. */
export interface EventType {
    name: string;
    description?: string;
    /** How the server delivers it: any of poll, push and webhook. */
    delivery: string[];
    /** JSON Schema of a subscription's arguments. */
    inputSchema?: Record<string, unknown>;
    /** JSON Schema of an occurrence's data. */
    payloadSchema?: Record<string, unknown>;
    _meta?: Record<string, unknown>;
}
/** One occurrence of an event. `eventId` is stable, so an occurrence delivered twice is yielded once. */
export interface EventOccurrence<Data = unknown> {
    eventId: string;
    name: string;
    timestamp: string;
    data: Data;
}
export interface SubscribeOptions {
    /** Ends the subscription. */
    signal?: AbortSignal;
    /** Where to resume from: a subscription's last cursor. Without one, the subscription starts from now. */
    cursor?: string | null;
    /** Do not replay events older than this many milliseconds. */
    maxAgeMs?: number;
    /** The delivery mode to use. Without one, push where the server offers it, and poll otherwise. One the server does not offer rejects with an error named NoCompatibleDeliveryMode. */
    delivery?: "push" | "poll";
    /** Called when the server skipped events, for example after it restarted. Read the current state again. */
    onGap?: () => void;
}
/** A subscription to an event: iterate it for the occurrences, in the order the server produced them. */
export interface Subscription<Data = unknown> extends AsyncIterable<EventOccurrence<Data>> {
    readonly delivery: "push" | "poll";
    /** The position after the last occurrence received, to resume from later. Null when the event cannot be replayed. */
    readonly cursor: string | null;
    /** Ends the subscription, as leaving a `for await` loop over it does. */
    cancel(): void;
}
/** Client operations shared by generated app clients. Transport, validation, and protocol errors reject the returned promises. A Blob at a file argument (x-mcp-file) is sent with the files extension: inline when the server takes it, else uploaded to the target the server names. A failure before the call completes says whether the tool did not run or the outcome is unknown. */
export interface ToolClient<I, O, R, E extends Record<string, {
    arguments: unknown;
    data: unknown;
}> = Record<string, {
    arguments: Record<string, unknown>;
    data: unknown;
}>> {
    /** Close the cached endpoint session. Scoped calls close their own session; a host-owned session is closed by its host. */
    readonly close: () => Promise<void>;
    /** Fetch the live tool catalog. */
    readonly listTools: (options?: CallOptions) => Promise<ListToolsResult>;
    /** Experimental MCP extensions. They follow drafts that are still changing, so what is here may change with them. */
    readonly ext: {
        /**
         * MCP Events, the draft extension of the Triggers & Events Working Group. subscribe requires an endpoint binding.
         * It resolves once the server has confirmed the subscription, then reconnects and resumes from its cursor by
         * itself, and rejects when the server offers no such event. A contract types an event's arguments and data by its
         * name, and its data is checked against the contract. Without one, the data is unknown.
         */
        readonly events: {
            readonly list: (options?: CallOptions) => Promise<EventType[]>;
            readonly subscribe: <K extends keyof E & string>(name: K, args: E[K]["arguments"], options?: SubscribeOptions) => Promise<Subscription<E[K]["data"]>>;
        };
    };
    /** Contracted JSON resources. read validates the first text content as JSON. watch requires an endpoint binding to a server that supports subscriptions; it reads the initial value, then listens for updates until aborted. */
    readonly resources: {
        readonly [U in keyof R]: {
            readonly read: (options?: CallOptions) => Promise<R[U]>;
            readonly watch: (onChange: (value: R[U]) => void | Promise<void>, options?: {
                signal?: AbortSignal;
            }) => Promise<void>;
        };
    };
    /** Convenience methods returning structured output, or the complete MCP result, with its content blocks typed, for a tool without an output schema. Error results throw ToolCallError, as do questions left unanswered. Reserved object method names remain callable through callTool. */
    readonly tools: {
        readonly [K in Exclude<keyof I, "then" | "toJSON" | "toString" | "valueOf" | "toLocaleString">]: (input: I[K], options?: ToolCallOptions) => Promise<K extends keyof O ? O[K] : CompleteResult<unknown>>;
    };
    /** Return the full MCP result, preserving error results and questions left unanswered. Successful output is contract-validated; the server validates arguments. */
    readonly callTool: <K extends keyof I & string>(request: CallMetadata & {
        name: K;
        arguments: I[K];
    }, options?: ToolCallOptions) => Promise<ToolResult<K extends keyof O ? O[K] : unknown>>;
}
export {};
