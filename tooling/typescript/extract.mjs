// Obtain an application's MCP contract by running its server and asking it.
//
// The entry and everything it imports are evaluated in a fresh vm context (see sandbox.mjs): an
// empty environment, a fixed clock and seeded randomness, so the contract is the same everywhere;
// no filesystem, network or timers. The application's own Zod and MCP SDK compute the schemas, or
// the tooling's copies when it has none.
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { createSandbox, MissingPackage } from "./sandbox.mjs";

const identity = { name: "holon-contract", version: "1" };
const resourceSchemaKey = "sh.holon/outputSchema";
const eventsExtension = "io.modelcontextprotocol/events";
// contract evaluates one entry in the sandbox and asks its server for the catalog.
export async function contract(root, entry) {
  const vendor = join(dirname(realpathSync(process.argv[1])), "vendor", "node_modules");
  const sandbox = createSandbox(root, vendor);
  try {
    const file = join(root, entry);
    const exported = (await sandbox.import(file)).default;
    let handler = exported;
    if (typeof exported?.fetch !== "function") {
      if (typeof exported?.registerTool !== "function") throw new Error("the default export is neither a createMcpHandler handler nor an McpServer");
      // Serve it with the SDK copy the entry imports, so its McpServer and handler match.
      const { createMcpHandler } = await sandbox.import(sandbox.resolve("@modelcontextprotocol/server", file));
      handler = createMcpHandler(() => exported);
    }
    return await catalog(handler);
  } catch (error) {
    return { diagnostic: `${entry}: ${sandbox.diagnostic(error)}`, ...(error instanceof MissingPackage && { missing: error.cause }) };
  }
}

async function catalog(handler) {
  // The modern protocol: on the legacy path the SDK server needs crypto, which the sandbox lacks.
  const client = new Client(identity, { versionNegotiation: { mode: "auto" } });
  try {
    await client.connect(new StreamableHTTPClientTransport(new URL("http://holon-contract/mcp"), {
      fetch: (url, init) => handler.fetch(new Request(url, init)),
    }));
    const capabilities = client.getServerCapabilities() ?? {};
    // Request the complete catalog without listTools' client-specific filtering.
    const tools = capabilities.tools ? await all(client, "tools/list", "tools") : [];
    const resources = capabilities.resources ? await all(client, "resources/list", "resources") : [];
    // MCP Events, the draft extension of the Triggers & Events Working Group, as holon-sh/events declares it.
    const events = capabilities.extensions?.[eventsExtension] ? await all(client, "events/list", "events", anything) : [];
    return {
      serverInfo: client.getServerVersion(),
      tools: tools.map(({ name, inputSchema, outputSchema }) => ({ name, inputSchema, ...(outputSchema && { outputSchema }) })).sort((a, b) => a.name.localeCompare(b.name)),
      // Like a live server's contract, a resource is typed only by its sh.holon/outputSchema.
      resources: resources.map(({ uri, _meta }) => ({ uri, outputSchema: _meta?.[resourceSchemaKey] ?? {} })).sort((a, b) => a.uri.localeCompare(b.uri)),
      // A type may leave either schema out: any arguments, and data of any shape.
      events: events.map(({ name, inputSchema, payloadSchema }) => ({ name, inputSchema: inputSchema ?? { type: "object" }, payloadSchema: payloadSchema ?? {} })).sort((a, b) => a.name.localeCompare(b.name)),
    };
  } finally {
    await client.close();
    await handler.close?.();
  }
}

// A method outside the specification needs a result schema; the contract checks what it returns.
const anything = { "~standard": { version: 1, vendor: "holon", validate: value => ({ value }) } };

async function all(client, method, key, schema) {
  const items = [];
  let cursor;
  do {
    const request = { method, params: cursor ? { cursor } : {} };
    const page = await (schema ? client.request(request, schema) : client.request(request));
    items.push(...page[key]);
    cursor = page.nextCursor;
  } while (cursor);
  return items;
}
