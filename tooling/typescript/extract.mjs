// Serve an application's MCP server over stdio, so the CLI can ask it for its contract.
//
// The entry and everything it imports are evaluated in a fresh vm context (see sandbox.mjs): an
// empty environment, a fixed clock and seeded randomness, so the contract is the same everywhere;
// no filesystem, network or timers. The application's own Zod and MCP SDK compute the schemas, or
// the tooling's copies when it has none.
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { createInterface } from "node:readline";
import { createSandbox, MissingPackage } from "./sandbox.mjs";

// serve evaluates one entry in the sandbox and answers each JSON-RPC message on stdin with its
// server, a line each on stdout, until stdin closes and every request has its answer. It returns a
// diagnostic when the entry fails.
export async function serve(root, entry) {
  const vendor = join(dirname(realpathSync(process.argv[1])), "vendor", "node_modules");
  const sandbox = createSandbox(root, vendor);
  let handler;
  try {
    const file = join(root, entry);
    const exported = (await sandbox.import(file)).default;
    handler = exported;
    if (typeof exported?.fetch !== "function") {
      if (typeof exported?.registerTool !== "function") throw new Error("the default export is neither a createMcpHandler handler nor an McpServer");
      // Serve it with the SDK copy the entry imports, so its McpServer and handler match.
      const { createMcpHandler } = await sandbox.import(sandbox.resolve("@modelcontextprotocol/server", file));
      handler = createMcpHandler(() => exported);
    }
    await relay(handler);
  } catch (error) {
    return { diagnostic: `${entry}: ${sandbox.diagnostic(error)}`, ...(error instanceof MissingPackage && { missing: error.cause }) };
  } finally {
    await handler?.close?.();
  }
}

// The modern protocol reaches the handler as HTTP requests: on the legacy path the SDK server needs
// crypto, which the sandbox lacks.
async function relay(handler) {
  const transport = new StreamableHTTPClientTransport(new URL("http://holon-contract/mcp"), {
    fetch: (url, init) => handler.fetch(new Request(url, init)),
  });
  const pending = new Map();
  transport.onmessage = message => {
    process.stdout.write(`${JSON.stringify(message)}\n`);
    pending.get(message.id)?.();
    pending.delete(message.id);
  };
  await transport.start();
  const answers = [];
  for await (const line of createInterface({ input: process.stdin, crlfDelay: Infinity })) {
    if (!line.trim()) continue;
    const message = JSON.parse(line);
    const request = message.method !== undefined && message.id !== undefined;
    if (request) answers.push(new Promise(resolve => pending.set(message.id, resolve)));
    try {
      await transport.send(message);
    } catch (error) {
      if (!request) throw error;
      transport.onmessage({ jsonrpc: "2.0", id: message.id, error: { code: -32603, message: String(error?.message ?? error) } });
    }
  }
  await Promise.all(answers);
  await transport.close();
}
