// Run an application's MCP server and record what it answers to the list requests the CLI names.
//
// The entry and everything it imports are evaluated in a fresh vm context (see sandbox.mjs): an
// empty environment, a fixed clock and seeded randomness, so the answers are the same everywhere;
// no filesystem, network or timers. The application's own Zod and MCP SDK compute the schemas, or
// the tooling's copies when it has none.
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { createSandbox, MissingPackage } from "./sandbox.mjs";

const anything = { "~standard": { version: 1, vendor: "holon", validate: value => ({ value }) } };

// contract evaluates one entry in the sandbox, connects to its server as a client with extensions,
// and pages through each method: the server's info and capabilities, and each method's pages or error.
export async function contract(root, entry, extensions, methods) {
  const vendor = join(dirname(realpathSync(process.argv[1])), "vendor", "node_modules");
  const sandbox = createSandbox(root, vendor);
  try {
    const file = join(root, entry);
    const exported = (await sandbox.import(file)).default;
    let handler = exported;
    if (typeof exported?.fetch !== "function") {
      if (typeof exported?.registerTool !== "function") throw new Error("the default export is neither a createMcpHandler handler nor an McpServer");
      const { createMcpHandler } = await sandbox.import(sandbox.resolve("@modelcontextprotocol/server", file));
      handler = createMcpHandler(async () => {
        await exported.close();
        return exported;
      });
    }
    const client = new Client({ name: "holon-contract", version: "1" }, { capabilities: { extensions }, versionNegotiation: { mode: "auto" } });
    try {
      await client.connect(new StreamableHTTPClientTransport(new URL("http://holon-contract/mcp"), {
        fetch: (url, init) => handler.fetch(new Request(url, init)),
      }));
      const lists = {};
      for (const method of methods) {
        try {
          const pages = [];
          for (let cursor; pages.length === 0 || cursor; cursor = pages.at(-1).nextCursor) {
            pages.push(await client.request({ method, params: cursor ? { cursor } : {} }, anything));
          }
          lists[method] = { pages };
        } catch (error) {
          lists[method] = { error: sandbox.diagnostic(error) };
        }
      }
      return { serverInfo: client.getServerVersion(), capabilities: client.getServerCapabilities() ?? {}, lists };
    } finally {
      await client.close();
      await handler.close?.();
    }
  } catch (error) {
    return { diagnostic: `${entry}: ${sandbox.diagnostic(error)}`, ...(error instanceof MissingPackage && { missing: error.cause }) };
  }
}
