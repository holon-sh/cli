// Obtain an application's MCP contract by running its server in-process and asking it.
//
// The entry is imported from the application directory, so its own node_modules supply Zod and
// the MCP SDK that compute the schemas. TypeScript entries load through Node's built-in type
// stripping (on by default since Node 22.18 and 23.6), which accepts erasable syntax and explicit
// `.ts` relative imports; enums, namespaces and parameter properties need transpiling first.
// HOLON_CONTRACT=1 is set during the import so applications can skip start-up side effects.
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import module from "node:module";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const identity = { name: "holon-contract", version: "1" };
const resourceSchemaKey = "sh.holon/outputSchema";
const timeout = 30000;

export async function extract(root, entry) {
  let timer;
  try {
    const expired = new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(`no catalog within ${timeout / 1000}s; gate start-up work on HOLON_CONTRACT`)), timeout); });
    return await Promise.race([catalog(resolve(root), entry), expired]);
  } catch (error) {
    return { diagnostic: `${entry}: ${error?.stack ?? error}` };
  } finally {
    clearTimeout(timer);
  }
}

async function catalog(root, entry) {
  if (/\.[mc]?ts$/.test(entry) && !process.features.typescript) throw new Error(`Node ${process.version} cannot import TypeScript; use Node 22.18 or newer`);
  process.env.HOLON_CONTRACT = "1";
  const exported = (await import(pathToFileURL(join(root, entry)).href)).default;
  let handler = exported;
  if (typeof exported?.fetch !== "function") {
    if (typeof exported?.registerTool !== "function") throw new Error("the default export is neither a createMcpHandler handler nor an McpServer");
    // Serve it with the application's own SDK, through the ESM build its McpServer came from.
    const sdk = module.findPackageJSON("@modelcontextprotocol/server", pathToFileURL(join(root, entry)));
    const main = JSON.parse(readFileSync(sdk, "utf8")).exports["."].import;
    const { createMcpHandler } = await import(pathToFileURL(join(dirname(sdk), main.default ?? main)).href);
    handler = createMcpHandler(() => exported);
  }
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
      tools: tools.map(({ name, inputSchema, outputSchema }) => ({ name, inputSchema, ...(outputSchema && { outputSchema }) })).sort((a, b) => a.name.localeCompare(b.name)),
      // Like a live server's contract, a resource is typed only by its sh.holon/outputSchema.
      resources: resources.map(({ uri, _meta }) => ({ uri, outputSchema: _meta?.[resourceSchemaKey] ?? {} })).sort((a, b) => a.uri.localeCompare(b.uri)),
      // The whole snapshot is the input; the entry digest lets check refuse a rebuilt entry.
      sources: { [entry]: createHash("sha256").update(readFileSync(join(root, entry))).digest("hex") },
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
