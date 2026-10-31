import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";
import { unzipSync } from "fflate";

const { version } = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
const archive = new URL(`../../../pkg/tooling/cli-tooling-ts-${version}.zip`, import.meta.url);

// Exercise the archive embedded by the CLI, outside the development dependency tree.
function unpackTooling(t) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "holon-tooling-")));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const tooling = join(root, "tooling");
  for (const [name, bytes] of Object.entries(unzipSync(readFileSync(archive)))) {
    mkdirSync(dirname(join(tooling, name)), { recursive: true });
    writeFileSync(join(tooling, name), bytes);
  }
  const run = (mode, config, output = [], env = {}) => execFileSync(process.execPath, [join(tooling, "runner.mjs"), mode, config, ...output], { encoding: "utf8", stdio: "pipe", timeout: 30000, env: { ...process.env, ...env } });
  return { root, run };
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

function extractApps(root, run, app, entries, env) {
  const config = join(root, "extract.json");
  const output = join(root, "output.json");
  writeFileSync(config, JSON.stringify({ apps: Object.entries(entries).map(([name, entry]) => ({ name, path: app, entry })) }));
  run("extract", config, [output], env);
  return JSON.parse(readFileSync(output, "utf8"));
}

const order = { $schema: "https://json-schema.org/draft/2020-12/schema", type: "object", properties: { id: { type: "string" }, status: { type: "string", enum: ["open", "closed"] } }, required: ["id", "status"], additionalProperties: false };

test("a server with only schema imports extracts with nothing installed and records every file served", t => {
  const { root, run } = unpackTooling(t);
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
import { z } from "zod";
import { list, read } from "./tools.js";
import { order } from "@schemas/order";
const prefix: string = "orders";
export default createMcpHandler(() => {
  const server = new McpServer({ name: "orders", version: "1" });
  server.registerTool(\`\${prefix}_read\`, read, () => { throw new Error("not called"); });
  server.registerTool(\`\${prefix}_list\`, list, () => ({ content: [] }));
  server.registerResource("Order", "orders://latest", { mimeType: "application/json", _meta: { "sh.holon/outputSchema": z.toJSONSchema(order) } }, () => ({ contents: [] }));
  server.registerResource("Page", "ui://orders/app.html", { mimeType: "text/html" }, () => ({ contents: [] }));
  return server;
}, { legacy: "reject" });`,
    // A plain McpServer default export is served through the same SDK copy's createMcpHandler.
    "plain.mjs": 'import { McpServer } from "@modelcontextprotocol/server"; export default new McpServer({ name: "plain", version: "1" });',
  });
  const extracted = extractApps(root, run, app, { orders: "server.ts", plain: "plain.mjs" });
  assert.equal(extracted.orders.diagnostic, undefined);
  assert.deepEqual(extracted.orders.tools.map(tool => tool.name), ["orders_list", "orders_read"]);
  assert.deepEqual(extracted.orders.tools[1].outputSchema, order);
  assert.deepEqual(extracted.orders.resources, [{ uri: "orders://latest", outputSchema: order }, { uri: "ui://orders/app.html", outputSchema: {} }]);
  assert.deepEqual(Object.keys(extracted.orders.sources), ["meta.json", "schemas/order.ts", "server.ts", "tools.ts"]);
  assert.deepEqual(extracted.plain, { tools: [], resources: [], sources: { "plain.mjs": extracted.plain.sources["plain.mjs"] } });

  const config = join(root, "verify.json");
  writeFileSync(config, JSON.stringify({ root: app, sources: extracted.orders.sources }));
  run("verify", config);
  writeFileSync(join(app, "dist.js"), "// a build output is not an input");
  run("verify", config);
  writeFileSync(join(app, "schemas/order.ts"), "export const status = null;");
  for (const mode of ["check", "verify"]) {
    assert.throws(() => run(mode, config), { stderr: /schemas\/order\.ts:.*stale/ });
  }
});

test("installed packages resolve from the application: its own Zod, CommonJS packages and its client slot", t => {
  const { root, run } = unpackTooling(t);
  const app = application(root, {
    "package.json": '{"type":"module"}',
    // Stands in for an installed Zod other than the bundled one; its JSON Schema names its version.
    "node_modules/zod/package.json": '{"name":"zod","version":"9.9.9","type":"module","exports":{".":"./index.js"}}',
    "node_modules/zod/index.js": `const schema = json => ({ "~standard": { version: 1, vendor: "zod", validate: value => ({ value }), jsonSchema: { input: () => json, output: () => json } } });
export const z = { object: shape => schema({ type: "object", description: "zod 9.9.9", properties: Object.fromEntries(Object.keys(shape).map(key => [key, shape[key]["~standard"].jsonSchema.input()])) }), enum: values => schema({ type: "string", enum: values }) };`,
    "node_modules/legacy-units/package.json": '{"name":"legacy-units","main":"index.js"}',
    "node_modules/legacy-units/index.js": 'const { names } = require("./names"); module.exports = { names, metric: true };',
    "node_modules/legacy-units/names.js": 'exports.names = ["cm", "in"];',
    "node_modules/holon-sh/package.json": '{"name":"holon-sh","type":"module","exports":{".":{"import":"./generated/index.js"}}}',
    "node_modules/holon-sh/generated/index.js": 'import { createApps } from "../runtime.mjs"; export const apps = createApps({ name: "unconfigured", version: "0", requires: {} }, {});',
    "node_modules/holon-sh/runtime.mjs": "export function createApps() { return {}; }",
    "server.ts": `import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { z } from "zod";
import units, { names } from "legacy-units";
import { apps } from "holon-sh";
export default createMcpHandler(() => {
  const server = new McpServer({ name: "units", version: "1" });
  if (units.metric && apps) server.registerTool("convert", { inputSchema: z.object({ unit: z.enum(names) }) }, () => ({ content: [] }));
  return server;
});`,
  });
  const { units } = extractApps(root, run, app, { units: "server.ts" });
  assert.equal(units.diagnostic, undefined);
  assert.deepEqual(units.tools, [{ name: "convert", inputSchema: { type: "object", description: "zod 9.9.9", properties: { unit: { type: "string", enum: ["cm", "in"] } } } }]);
  assert.deepEqual(Object.keys(units.sources), ["server.ts"]);
});

test("module-scope effects fail with the capability, file and line; missing packages are named", t => {
  const { root, run } = unpackTooling(t);
  const app = application(root, {
    "package.json": '{"type":"module"}',
    "env.ts": 'interface Settings {\n  key: string;\n}\nconst settings: Settings = { key: process.env.API_KEY ?? "" };\nexport default settings;\n',
    "fetch.ts": 'const response: unknown = null;\nawait fetch("https://example.com/schema.json");\nexport default response;\n',
    "fs.ts": 'import { readFileSync } from "node:fs";\nexport default readFileSync("schema.json", "utf8");\n',
    "missing.ts": 'import pad from "left-pad";\nexport default pad;\n',
    "throw.mjs": 'throw new Error("database unavailable");',
    "hang.mjs": "while (true) {}",
  });
  const extracted = extractApps(root, run, app, { env: "env.ts", fetch: "fetch.ts", fs: "fs.ts", missing: "missing.ts", broken: "throw.mjs" });
  assert.match(extracted.env.diagnostic, /^env\.ts: ReferenceError: process is not defined: contract evaluation has no process environment\n\s+at \S*\/env\.ts:4:/);
  assert.match(extracted.fetch.diagnostic, /^fetch\.ts: ReferenceError: fetch is not defined: contract evaluation has no network\n\s+at \S*\/fetch\.ts:2:/);
  assert.match(extracted.fs.diagnostic, /^fs\.ts: Error: node:fs readFileSync is unavailable: contract evaluation has no filesystem\n[\s\S]*\/fs\.ts:2:/);
  assert.match(extracted.missing.diagnostic, /^missing\.ts: Error: Cannot find package left-pad imported from \S*\/missing\.ts; install the application's dependencies or commit holon-contract\.json/);
  assert.equal(extracted.missing.missing, "left-pad");
  assert.match(extracted.broken.diagnostic, /^throw\.mjs: Error: database unavailable\n\s+at \S*\/throw\.mjs:1:/);
  const { hung } = extractApps(root, run, app, { hung: "hang.mjs" }, { HOLON_CONTRACT_TIMEOUT: "1" });
  assert.equal(hung.diagnostic, "hang.mjs: no catalog within 1s; raise HOLON_CONTRACT_TIMEOUT if module-scope work needs longer");
});

test("check uses the application's compiler CLI and honors project file selection", t => {
  const { root, run } = unpackTooling(t);
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
