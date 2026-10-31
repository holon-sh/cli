// Obtain an application's MCP contract by running its server and asking it.
//
// Each application runs in its own child process, so apps in a batch share no globals,
// environment or event loop, and a hung import is killed after HOLON_CONTRACT_TIMEOUT seconds
// (30 by default). The entry is imported from the application directory, so its own node_modules
// supply Zod and the MCP SDK that compute the schemas. TypeScript loads through the application's
// own tsx when it has one (so `./tools.js` imports and tsconfig paths resolve), else through
// Node's built-in type stripping (Node 22.18+), which needs erasable syntax and `.ts` imports.
// HOLON_CONTRACT=1 is set so applications can skip start-up side effects.
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { fork } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import module from "node:module";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const identity = { name: "holon-contract", version: "1" };
const resourceSchemaKey = "sh.holon/outputSchema";

export function extract(root, entry) {
  const seconds = Number(process.env.HOLON_CONTRACT_TIMEOUT) || 30;
  // The application directory is the working directory, as when the server starts, so tsx finds its tsconfig.
  const child = fork(process.argv[1], ["contract", resolve(root), entry], { cwd: root, env: { ...process.env, HOLON_CONTRACT: "1" } });
  return new Promise(done => {
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      done({ diagnostic: `${entry}: no catalog within ${seconds}s; gate start-up work on HOLON_CONTRACT or raise HOLON_CONTRACT_TIMEOUT` });
    }, seconds * 1000);
    child.on("message", result => { clearTimeout(timer); done(result); });
    child.on("exit", (code, signal) => { clearTimeout(timer); done({ diagnostic: `${entry}: exited (${signal ?? code}) before answering` }); });
  });
}

// contract runs in the child: it imports one entry and asks its server for the catalog.
export async function contract(root, entry) {
  try {
    const url = pathToFileURL(join(root, entry));
    const tsx = appModule("tsx", "./esm/api", url);
    if (tsx) (await import(tsx)).register();
    else if (/\.[mc]?ts$/.test(entry) && !process.features.typescript) throw new Error(`Node ${process.version} cannot import TypeScript; use Node 22.18 or newer, or install tsx`);
    const exported = (await import(url.href)).default;
    let handler = exported;
    if (typeof exported?.fetch !== "function") {
      if (typeof exported?.registerTool !== "function") throw new Error("the default export is neither a createMcpHandler handler nor an McpServer");
      // Serve it with the application's own SDK, through the ESM build its McpServer came from.
      const { createMcpHandler } = await import(appModule("@modelcontextprotocol/server", ".", url));
      handler = createMcpHandler(() => exported);
    }
    return { ...await catalog(handler), sources: { [entry]: createHash("sha256").update(readFileSync(join(root, entry))).digest("hex") } };
  } catch (error) {
    return { diagnostic: `${entry}: ${error?.stack ?? error}` };
  }
}

// appModule is the ESM file of an application dependency's export, or undefined when absent.
function appModule(name, subpath, parent) {
  let manifest;
  try { manifest = module.findPackageJSON(name, parent); } catch { return; }
  const target = JSON.parse(readFileSync(manifest, "utf8")).exports[subpath].import;
  return pathToFileURL(join(dirname(manifest), target.default ?? target)).href;
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
