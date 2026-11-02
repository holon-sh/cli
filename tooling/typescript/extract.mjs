// Obtain an application's MCP contract by running its server and asking it.
//
// The entry and everything it imports are evaluated in a fresh vm context (see sandbox.mjs): an
// empty environment, a fixed clock and seeded randomness, so the contract is the same everywhere;
// no filesystem, network or timers. The application's own Zod and MCP SDK compute the schemas, or
// the tooling's copies when it has none.
// The files the loader serves from the application tree are recorded as `sources`, the inputs check
// and verify require to be unchanged.
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { createHash } from "node:crypto";
import { readFileSync, realpathSync } from "node:fs";
import { dirname, join, relative, sep } from "node:path";
import { createSandbox, MissingPackage } from "./sandbox.mjs";
import { functionContract } from "./functions.mjs";

const identity = { name: "holon-contract", version: "1" };
const resourceSchemaKey = "sh.holon/outputSchema";
// contract evaluates one entry in the sandbox and asks its server for the catalog.
export async function contract(root, entry) {
  const vendor = join(dirname(realpathSync(process.argv[1])), "vendor", "node_modules");
  const real = realpathSync(root);
  let app;
  const sandbox = createSandbox(root, vendor, path => {
    if (!/[\\/]holon-sh[\\/]generated[\\/]app\.json$/.test(path)) return;
    if (!app) {
      const { contract, files } = functionContract(real, entry);
      app = contract;
      for (const file of files) sandbox.served.add(file);
    }
    return JSON.stringify(app);
  });
  try {
    const file = join(root, entry);
    const { default: exported, publishing } = await sandbox.import(file);
    let handler = exported;
    if (typeof exported?.fetch !== "function") {
      if (typeof exported?.registerTool !== "function") throw new Error("the default export is neither a createMcpHandler handler nor an McpServer");
      // Serve it with the SDK copy the entry imports, so its McpServer and handler match.
      const { createMcpHandler } = await sandbox.import(sandbox.resolve("@modelcontextprotocol/server", file));
      handler = createMcpHandler(() => exported);
    }
    const result = await catalog(handler, publishing);
    const files = [...sandbox.served].map(served => relative(real, served)).filter(path => !path.startsWith("..") && !path.split(sep).includes("node_modules")).sort();
    return { ...result, app, sources: Object.fromEntries(files.map(path => [path.split(sep).join("/"), createHash("sha256").update(readFileSync(join(real, path))).digest("hex")])) };
  } catch (error) {
    return { diagnostic: `${entry}: ${sandbox.diagnostic(error)}`, ...(error instanceof MissingPackage && { missing: error.cause }) };
  }
}

async function catalog(handler, publishing) {
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
    return {
      descriptor: { ...publishing, ...client.getServerVersion() },
      tools: tools.map(({ name, inputSchema, outputSchema }) => ({ name, inputSchema, ...(outputSchema && { outputSchema }) })).sort((a, b) => a.name.localeCompare(b.name)),
      // Like a live server's contract, a resource is typed only by its sh.holon/outputSchema.
      resources: resources.map(({ uri, _meta }) => ({ uri, outputSchema: _meta?.[resourceSchemaKey] ?? {} })).sort((a, b) => a.uri.localeCompare(b.uri)),
    };
  } finally {
    await client.close();
    await handler.close?.();
  }
}

async function all(client, method, key) {
  const items = [];
  let cursor;
  do {
    const page = await client.request({ method, params: cursor ? { cursor } : {} });
    items.push(...page[key]);
    cursor = page.nextCursor;
  } while (cursor);
  return items;
}
