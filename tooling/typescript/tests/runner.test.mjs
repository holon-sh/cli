import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, realpathSync, symlinkSync } from "node:fs";
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
  const run = (mode, config, output = []) => execFileSync(process.execPath, [join(tooling, "runner.mjs"), mode, config, ...output], { encoding: "utf8", stdio: "pipe", timeout: 30000 });
  return { root, run };
}

// An application whose own node_modules hold the SDK and Zod, linked from the tooling's.
function application(root, files) {
  const app = join(root, "application");
  for (const name of ["@modelcontextprotocol/server", "zod"]) {
    mkdirSync(dirname(join(app, "node_modules", name)), { recursive: true });
    symlinkSync(fileURLToPath(new URL(`../node_modules/${name}`, import.meta.url)), join(app, "node_modules", name));
  }
  for (const [name, source] of Object.entries(files)) writeFileSync(join(app, name), source);
  return app;
}

function extractApps(root, run, apps) {
  const config = join(root, "extract.json");
  const output = join(root, "output.json");
  writeFileSync(config, JSON.stringify({ apps }));
  run("extract", config, [output]);
  return JSON.parse(readFileSync(output, "utf8"));
}

test("the standalone runner asks the application's own server for its catalog and rejects stale entries", t => {
  const { root, run } = unpackTooling(t);
  const app = application(root, {
    "package.json": '{"type":"module"}',
    "schemas.ts": 'import { z } from "zod"; export const status = z.enum(["open", "closed"]); export const order = z.object({ id: z.string(), status });',
    // Template literals, imported schemas and conditional registration are ordinary code now.
    "server.ts": `import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { z } from "zod";
import { order, status } from "./schemas.ts";
const prefix: string = "orders";
// Read at import time: HOLON_CONTRACT lets applications skip start-up side effects.
const contract = process.env.HOLON_CONTRACT === "1";
export default createMcpHandler(() => {
  const server = new McpServer({ name: "orders", version: "1" });
  server.registerTool(\`\${prefix}_read\`, { inputSchema: z.object({ id: z.string() }), outputSchema: order }, () => { throw new Error("not called"); });
  if (contract) server.registerTool("contract_only", { inputSchema: z.object({ status }) }, () => ({ content: [] }));
  server.registerResource("Order", "orders://latest", { mimeType: "application/json", _meta: { "sh.holon/outputSchema": z.toJSONSchema(order) } }, () => ({ contents: [] }));
  server.registerResource("Page", "ui://orders/app.html", { mimeType: "text/html" }, () => ({ contents: [] }));
  return server;
}, { legacy: "reject" });`,
    // A plain McpServer default export is served through the application's createMcpHandler.
    "plain.mjs": 'import { McpServer } from "@modelcontextprotocol/server"; const server = new McpServer({ name: "plain", version: "1" }); export default server;',
  });
  const extracted = extractApps(root, run, [{ name: "orders", path: app, entry: "server.ts" }, { name: "plain", path: app, entry: "plain.mjs" }]);
  const order = { $schema: "https://json-schema.org/draft/2020-12/schema", type: "object", properties: { id: { type: "string" }, status: { type: "string", enum: ["open", "closed"] } }, required: ["id", "status"], additionalProperties: false };
  assert.deepEqual(extracted.orders.tools.map(tool => tool.name), ["contract_only", "orders_read"]);
  assert.deepEqual(extracted.orders.tools[1].outputSchema, order);
  assert.deepEqual(extracted.orders.resources, [{ uri: "orders://latest", outputSchema: order }, { uri: "ui://orders/app.html", outputSchema: {} }]);
  assert.deepEqual(Object.keys(extracted.orders.sources), ["server.ts"]);
  assert.deepEqual(extracted.plain, { tools: [], resources: [], sources: extracted.plain.sources });

  const config = join(root, "verify.json");
  writeFileSync(config, JSON.stringify({ root: app, sources: extracted.orders.sources }));
  run("verify", config);
  writeFileSync(join(app, "server.ts"), "export default null;");
  for (const mode of ["check", "verify"]) {
    assert.throws(() => run(mode, config), { stderr: /server\.ts:.*stale/ });
  }
});

test("an entry that throws while importing becomes a diagnostic", t => {
  const { root, run } = unpackTooling(t);
  const app = application(root, { "server.mjs": 'throw new Error("database unavailable");' });
  const extracted = extractApps(root, run, [{ name: "broken", path: app, entry: "server.mjs" }]);
  assert.match(extracted.broken.diagnostic, /^server\.mjs: Error: database unavailable\n\s+at /);
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
