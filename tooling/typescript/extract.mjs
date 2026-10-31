// Obtain an application's MCP contract by running its server in a capability-stripped sandbox and
// asking it.
//
// Each application runs in its own child process, killed after HOLON_CONTRACT_TIMEOUT seconds (30
// by default). There the entry and everything it imports are evaluated in a fresh vm context (see
// sandbox.mjs): module-scope code has no filesystem, network, environment, timers or clock, and the
// application's own Zod and MCP SDK compute the schemas, or the tooling's copies when it has none.
// The files the loader serves from the application tree are recorded as `sources`, the inputs check
// and verify require to be unchanged.
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { fork } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync, realpathSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { createSandbox, MissingPackage } from "./sandbox.mjs";

const identity = { name: "holon-contract", version: "1" };
const resourceSchemaKey = "sh.holon/outputSchema";

export function extract(root, entry) {
  const seconds = Number(process.env.HOLON_CONTRACT_TIMEOUT) || 30;
  const child = fork(process.argv[1], ["contract", resolve(root), entry], { execArgv: ["--experimental-vm-modules", "--no-warnings"] });
  return new Promise(done => {
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      done({ diagnostic: `${entry}: no catalog within ${seconds}s; raise HOLON_CONTRACT_TIMEOUT if module-scope work needs longer` });
    }, seconds * 1000);
    child.on("message", result => { clearTimeout(timer); done(result); });
    child.on("exit", (code, signal) => { clearTimeout(timer); done({ diagnostic: `${entry}: exited (${signal ?? code}) before answering` }); });
  });
}

// contract runs in the child: it evaluates one entry in the sandbox and asks its server for the catalog.
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
    const result = await catalog(handler);
    const real = realpathSync(root);
    const files = [...sandbox.served].map(served => relative(real, served)).filter(path => !path.startsWith("..") && !path.split(sep).includes("node_modules")).sort();
    return { ...result, sources: Object.fromEntries(files.map(path => [path.split(sep).join("/"), createHash("sha256").update(readFileSync(join(real, path))).digest("hex")])) };
  } catch (error) {
    return { diagnostic: `${entry}: ${sandbox.diagnostic(error)}`, ...(error instanceof MissingPackage && { missing: error.cause }) };
  }
}

async function catalog(handler) {
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
