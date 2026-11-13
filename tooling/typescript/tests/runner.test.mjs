import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, realpathSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync, spawnSync } from "node:child_process";
import { unzipSync } from "fflate";

const archive = new URL("../../../pkg/tooling/cli-tooling-ts.zip", import.meta.url);

// Exercise the archive embedded by the CLI, outside the development dependency tree.
function unpackTooling(t) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "holon-tooling-")));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const tooling = join(root, "tooling");
  for (const [name, bytes] of Object.entries(unzipSync(readFileSync(archive)))) {
    mkdirSync(dirname(join(tooling, name)), { recursive: true });
    writeFileSync(join(tooling, name), bytes);
  }
  const check = (cwd, entry) => execFileSync(process.execPath, [join(tooling, "check.mjs"), "check", entry], { cwd, encoding: "utf8", stdio: "pipe" });
  // extract returns the tools each app's server lists, or its diagnostic.
  const extract = (apps, env = process.env) => Object.fromEntries(apps.map(({ name, path, entry }) => {
    const { status, stdout, stderr } = spawnSync(process.execPath, [join(tooling, "runner.mjs"), "contract", path, entry, "{}", "tools/list"], { encoding: "utf8", env });
    assert.ok([0, 2, 3].includes(status), stderr);
    if (status !== 0) return [name, { diagnostic: stderr }];
    const { pages, error } = JSON.parse(stdout).lists["tools/list"];
    return [name, { tools: pages?.flatMap(page => page.tools) ?? [], diagnostic: error }];
  }));
  return { root, check, extract };
}

// installCompiler gives app a compiler package that exposes bin.tsc without a JavaScript API, as
// TypeScript 7 does.
function installCompiler(app) {
  const compiler = join(app, "node_modules/typescript");
  mkdirSync(compiler, { recursive: true });
  writeFileSync(join(compiler, "package.json"), JSON.stringify({ bin: { tsc: "compiler.cjs" }, exports: { "./package.json": "./package.json" } }));
  writeFileSync(join(compiler, "compiler.cjs"), `require(${JSON.stringify(fileURLToPath(new URL("../node_modules/typescript/bin/tsc", import.meta.url)))});`);
}

// An application directory holding files, including any node_modules it has installed.
function application(root, files) {
  const app = join(root, "application");
  for (const [name, source] of Object.entries(files)) {
    mkdirSync(dirname(join(app, name)), { recursive: true });
    writeFileSync(join(app, name), source);
  }
  return app;
}

test("a server with only schema imports extracts with nothing installed", t => {
  const { root, extract } = unpackTooling(t);
  const app = application(root, {
    "package.json": '{"type":"module"}',
    "tsconfig.json": JSON.stringify({ compilerOptions: { baseUrl: ".", paths: { "@schemas/*": ["./schemas/*"] } } }),
    "schemas/order.ts": 'import { z } from "zod"; export const status = z.enum(["open", "closed"]); export const order = z.object({ id: z.string(), status });',
    "meta.json": '{"description": "Read an order"}',
    "tools.ts": `import { z } from "zod";
import { order, status } from "@schemas/order";
import meta from "./meta.json" with { type: "json" };
export const read = { description: meta.description, inputSchema: z.object({ id: z.string() }), outputSchema: order };
export const list = { inputSchema: z.object({ status }) };`,
    "server.ts": `import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { list, read } from "./tools.js";
export default createMcpHandler(() => {
  const server = new McpServer({ name: "orders", version: "1" });
  server.registerTool("read", read, () => { throw new Error("not called"); });
  server.registerTool("list", list, () => ({ content: [] }));
  return server;
}, { legacy: "reject" });`,
  });
  const extracted = extract([{ name: "orders", path: app, entry: "server.ts" }]);
  const [read, list] = extracted.orders.tools;
  assert.deepEqual([read.name, read.description, list.name], ["read", "Read an order", "list"]);
  assert.deepEqual(Object.keys(read.outputSchema.properties), ["id", "status"]);
});

test("extraction uses the paths of a tsconfig in a parent directory, relative to that config", t => {
  const { root, extract } = unpackTooling(t);
  const app = application(root, {
    "tsconfig.json": JSON.stringify({ compilerOptions: { paths: { "@schemas/*": ["./packages/schemas/*"] } } }),
    "packages/schemas/order.ts": 'import { z } from "zod"; export const order = z.object({ id: z.string() });',
    "packages/api/package.json": '{"type":"module"}',
    "packages/api/server.ts": `import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { order } from "@schemas/order";
export default createMcpHandler(() => {
  const server = new McpServer({ name: "orders", version: "1" });
  server.registerTool("read", { inputSchema: order }, () => ({ content: [] }));
  return server;
}, { legacy: "reject" });`,
  });
  const { api } = extract([{ name: "api", path: join(app, "packages", "api"), entry: "server.ts" }]);
  assert.deepEqual(Object.keys(api.tools?.[0].inputSchema.properties ?? {}), ["id"], api.diagnostic);
});

test("installed packages resolve from the application: its own Zod, CommonJS packages and the MCP client", t => {
  const { root, extract } = unpackTooling(t);
  const app = application(root, {
    "package.json": '{"type":"module"}',
    // Stands in for an installed Zod other than the bundled one; its JSON Schema names its version.
    "node_modules/zod/package.json": '{"name":"zod","version":"9.9.9","type":"module","exports":{".":"./index.js"}}',
    "node_modules/zod/index.js": `const schema = json => ({ "~standard": { version: 1, vendor: "zod", validate: value => ({ value }), jsonSchema: { input: () => json, output: () => json } } });
export const z = { object: shape => schema({ type: "object", description: "zod 9.9.9", properties: Object.fromEntries(Object.keys(shape).map(key => [key, shape[key]["~standard"].jsonSchema.input()])) }), enum: values => schema({ type: "string", enum: values }) };`,
    "node_modules/legacy-units/package.json": '{"name":"legacy-units","main":"index.js","exports":{".":"./index.js","./names":"./names.js"}}',
    "node_modules/legacy-units/index.js": 'const { names } = require("./names.js"); module.exports = { names, metric: true };',
    "node_modules/legacy-units/names.js": 'exports.names = ["cm", "in"];',
    // A TypeScript decoy beside the published JavaScript: the running server uses names.js.
    "node_modules/legacy-units/names.ts": 'export const names = ["cm", "in", "ft"];',
    "server.ts": `import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { z } from "zod";
import units from "legacy-units";
import { names } from "legacy-units/names";
import { Client } from "@modelcontextprotocol/client";
export default createMcpHandler(() => {
  const server = new McpServer({ name: "units", version: "1" });
  if (units.metric && Client) server.registerTool("convert", { inputSchema: z.object({ unit: z.enum(names) }) }, () => ({ content: [] }));
  return server;
});`,
  });
  // Servers that call other apps import the MCP client.
  mkdirSync(join(app, "node_modules/@modelcontextprotocol"));
  symlinkSync(fileURLToPath(new URL("../node_modules/@modelcontextprotocol/client", import.meta.url)), join(app, "node_modules/@modelcontextprotocol/client"));
  const { units } = extract([{ name: "units", path: app, entry: "server.ts" }]);
  assert.deepEqual(units.tools[0].inputSchema, { type: "object", description: "zod 9.9.9", properties: { unit: { type: "string", enum: ["cm", "in"] } } });
});

test("a server that imports its generated clients extracts before holon has written them", t => {
  const { root, extract } = unpackTooling(t);
  const app = application(root, {
    "package.json": '{"type":"module","imports":{"#holon-sh":"./generated/holon-sh/index.mjs"}}',
    "tsconfig.json": JSON.stringify({ compilerOptions: { paths: { "@/*": ["./*"] } } }),
    "node_modules/holon-sh/package.json": '{"name":"holon-sh","type":"module","exports":{"./runtime":"./runtime.mjs"}}',
    "node_modules/holon-sh/runtime.mjs": `import { AsyncLocalStorage } from "node:async_hooks";
const holder = new AsyncLocalStorage();
export class ToolCallError extends Error {}
export const createApps = (identity, contracts) => Object.freeze(Object.fromEntries(Object.keys(contracts).map(name => [name, {}])));
export const bindApps = (identity, contracts, bindings) => createApps(identity, contracts);`,
    "src/server.ts": `import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { apps, ToolCallError } from "../generated/holon-sh/index.mjs";
import { createApps } from "@/generated/holon-sh/index.mjs";
import { apps as mapped } from "#holon-sh";
export default createMcpHandler(() => {
  const server = new McpServer({ name: "caller", version: "1" });
  if (ToolCallError && Object.keys({ ...apps, ...mapped, ...createApps({}) }).length === 0) server.registerTool("relay", {}, () => ({ content: [] }));
  return server;
}, { legacy: "reject" });`,
  });
  const { caller } = extract([{ name: "caller", path: app, entry: "src/server.ts" }]);
  assert.deepEqual(caller.tools?.map(tool => tool.name), ["relay"], caller.diagnostic);
});

test("module-scope reads of the environment, clock and randomness give one contract", t => {
  const { root, extract } = unpackTooling(t);
  const app = application(root, {
    "package.json": '{"type":"module"}',
    "server.ts": `import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { z } from "zod";
const seen = \`\${process.env.API_KEY} \${Date.now()} \${new Date().toString()} \${Math.random()}\`;
export default createMcpHandler(() => {
  const server = new McpServer({ name: "frozen", version: "1" });
  server.registerTool("read", { inputSchema: z.object({ seen: z.string().default(seen) }) }, () => ({ content: [] }));
  return server;
});`,
  });
  const [first, second] = ["first", "second"].map(key => extract([{ name: "frozen", path: app, entry: "server.ts" }], { ...process.env, API_KEY: key, TZ: key === "first" ? "Asia/Tokyo" : "America/Denver" }).frozen);
  assert.deepEqual(first.tools.map(tool => tool.name), ["read"]);
  assert.deepEqual(first, second);
});

test("module-scope effects fail with the capability, file and line", t => {
  const { root, extract } = unpackTooling(t);
  const app = application(root, {
    "package.json": '{"type":"module"}',
    "fetch.ts": 'const response: unknown = null;\nawait fetch("https://example.com/schema.json");\nexport default response;\n',
    "fs.ts": 'import { readFileSync } from "node:fs";\nexport default readFileSync("schema.json", "utf8");\n',
  });
  const extracted = extract(["fetch.ts", "fs.ts"].map(entry => ({ name: entry.slice(0, -3), path: app, entry })));
  assert.match(extracted.fetch.diagnostic, /no network[\s\S]*\/fetch\.ts:2:/);
  assert.match(extracted.fs.diagnostic, /no filesystem[\s\S]*\/fs\.ts:2:/);
});

test("check uses the application's compiler CLI and honors project file selection", t => {
  const { root, check } = unpackTooling(t);
  const app = join(root, "application");
  mkdirSync(app);
  writeFileSync(join(app, "package.json"), '{"type":"module"}');
  installCompiler(app);
  writeFileSync(join(app, "check.ts"), "export const total = 1;");
  writeFileSync(join(app, "tsconfig.json"), JSON.stringify({ compilerOptions: { target: "ES2023", module: "ESNext", moduleResolution: "Bundler", strict: true }, files: ["check.ts"] }));
  // The application's file selection takes precedence over entry/discovery.
  writeFileSync(join(app, "excluded.ts"), 'export const wrong: number = "excluded";');
  check(app, "excluded.ts");
});

test("check finds a tsconfig as tsc does, and needs one unless it is named an entry", t => {
  const { root, check } = unpackTooling(t);
  const app = application(root, { "package.json": '{"type":"module"}', "packages/api/package.json": '{"type":"module"}', "packages/api/wrong.ts": 'export const wrong: number = "wrong";' });
  installCompiler(app);
  const project = join(app, "packages", "api");
  const reportsWrong = error => error.stdout.includes("wrong.ts");
  assert.throws(() => check(project, ""), error => error.stderr.includes("no tsconfig.json"));
  assert.throws(() => check(project, "wrong.ts"), reportsWrong);
  writeFileSync(join(app, "tsconfig.json"), JSON.stringify({ compilerOptions: { strict: true, target: "ES2023", module: "NodeNext", moduleResolution: "NodeNext", types: [] } }));
  assert.throws(() => check(project, ""), reportsWrong);
});
