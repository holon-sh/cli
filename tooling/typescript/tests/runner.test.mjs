import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, realpathSync } from "node:fs";
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
  const run = (mode, ...args) => execFileSync(process.execPath, [join(tooling, "runner.mjs"), mode, ...args], { encoding: "utf8", stdio: "pipe" });
  // extract returns each app's contract, or its diagnostic and the package it missed.
  const extract = (apps, env = process.env) => Object.fromEntries(apps.map(({ name, path, entry }) => {
    const { status, stdout, stderr } = spawnSync(process.execPath, [join(tooling, "runner.mjs"), "contract", path, entry], { encoding: "utf8", env });
    assert.ok([0, 2, 3].includes(status), stderr);
    return [name, status === 0 ? JSON.parse(stdout) : { diagnostic: stderr, missing: status === 3 ? stderr.trimEnd().split("\n").at(-1) : undefined }];
  }));
  return { root, run, extract };
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

test("a server with only schema imports extracts with nothing installed and records every file served", t => {
  const { root, run, extract } = unpackTooling(t);
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
  server.registerResource("Order", "orders://latest", { mimeType: "application/json", _meta: { "sh.holon/outputSchema": { type: "object" } } }, () => ({ contents: [] }));
  server.registerResource("Page", "ui://orders/app.html", { mimeType: "text/html" }, () => ({ contents: [] }));
  return server;
}, { legacy: "reject" });`,
    // A plain McpServer default export is served through the same SDK copy's createMcpHandler.
    "plain.mjs": 'import { McpServer } from "@modelcontextprotocol/server"; export default new McpServer({ name: "plain", version: "1" });',
  });
  const extracted = extract([{ name: "orders", path: app, entry: "server.ts" }, { name: "plain", path: app, entry: "plain.mjs" }]);
  assert.deepEqual(extracted.orders.tools.map(tool => tool.name), ["list", "read"]);
  assert.deepEqual(Object.keys(extracted.orders.tools[1].outputSchema.properties), ["id", "status"]);
  assert.deepEqual(extracted.orders.resources, [{ uri: "orders://latest", outputSchema: { type: "object" } }, { uri: "ui://orders/app.html", outputSchema: {} }]);
  assert.deepEqual(Object.keys(extracted.orders.sources), ["meta.json", "schemas/order.ts", "server.ts", "tools.ts"]);
  assert.deepEqual(extracted.plain.tools, []);

  const config = join(root, "verify.json");
  writeFileSync(config, JSON.stringify({ root: app, sources: extracted.orders.sources }));
  run("verify", config);
  writeFileSync(join(app, "schemas/order.ts"), "export const status = null;");
  for (const mode of ["check", "verify"]) {
    assert.throws(() => run(mode, config), { stderr: /schemas\/order\.ts:.*stale/ });
  }
});

test("installed packages resolve from the application: its own Zod and CommonJS packages", t => {
  const { root, run, extract } = unpackTooling(t);
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
export default createMcpHandler(() => {
  const server = new McpServer({ name: "units", version: "1" });
  if (units.metric) server.registerTool("convert", { inputSchema: z.object({ unit: z.enum(names) }) }, () => ({ content: [] }));
  return server;
});`,
  });
  const { units } = extract([{ name: "units", path: app, entry: "server.ts" }]);
  assert.deepEqual(units.tools, [{ name: "convert", inputSchema: { type: "object", description: "zod 9.9.9", properties: { unit: { type: "string", enum: ["cm", "in"] } } } }]);
  assert.deepEqual(Object.keys(units.sources), ["server.ts"]);
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

test("module-scope effects fail with the capability, file and line; missing packages are named", t => {
  const { root, extract } = unpackTooling(t);
  const app = application(root, {
    "package.json": '{"type":"module"}',
    "fetch.ts": 'const response: unknown = null;\nawait fetch("https://example.com/schema.json");\nexport default response;\n',
    "fs.ts": 'import { readFileSync } from "node:fs";\nexport default readFileSync("schema.json", "utf8");\n',
    "missing.ts": 'import pad from "left-pad";\nexport default pad;\n',
  });
  const extracted = extract(["fetch.ts", "fs.ts", "missing.ts"].map(entry => ({ name: entry.slice(0, -3), path: app, entry })));
  assert.match(extracted.fetch.diagnostic, /no network[\s\S]*\/fetch\.ts:2:/);
  assert.match(extracted.fs.diagnostic, /no filesystem[\s\S]*\/fs\.ts:2:/);
  assert.equal(extracted.missing.missing, "left-pad");
});

test("check uses the application's compiler CLI and honors project file selection", t => {
  const { root, run, extract } = unpackTooling(t);
  const app = join(root, "application");
  mkdirSync(app);
  writeFileSync(join(app, "package.json"), '{"type":"module"}');
  // A compiler package can expose bin.tsc without a JavaScript API, as TypeScript 7 does.
  const compiler = join(app, "node_modules/typescript");
  mkdirSync(compiler, { recursive: true });
  writeFileSync(join(compiler, "package.json"), JSON.stringify({ bin: { tsc: "compiler.cjs" }, exports: { "./package.json": "./package.json" } }));
  writeFileSync(join(compiler, "compiler.cjs"), `require(${JSON.stringify(fileURLToPath(new URL("../node_modules/typescript/bin/tsc", import.meta.url)))});`);
  writeFileSync(join(app, "check.ts"), "export const total = 1;");
  const config = join(root, "check.json");
  writeFileSync(join(app, "tsconfig.json"), JSON.stringify({ compilerOptions: { target: "ES2023", module: "ESNext", moduleResolution: "Bundler", strict: true }, files: ["check.ts"] }));
  // The application's file selection takes precedence over entry/discovery.
  writeFileSync(join(app, "excluded.ts"), 'export const wrong: number = "excluded";');
  writeFileSync(config, JSON.stringify({ root: app, entry: "excluded.ts" }));
  run("check", config);
});
