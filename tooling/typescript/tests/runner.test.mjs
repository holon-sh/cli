import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, symlinkSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
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
  const run = (mode, config, output = []) => spawnSync(process.execPath, [join(tooling, "runner.mjs"), mode, config, ...output], { encoding: "utf8", timeout: 30000 });
  return { root, run };
}

test("the standalone runner extracts a batch without installed dependencies and rejects stale contracts", t => {
  const { root, run } = unpackTooling(t);
  const app = join(root, "application");
  mkdirSync(app);
  const inputSchema = {type: "object", properties: {id: {type: "string"}}, required: ["id"]};
  writeFileSync(join(app, "input.json"), JSON.stringify(inputSchema));
  writeFileSync(join(app, "server.ts"), 'import {McpServer,createMcpHandler,fromJsonSchema} from "@modelcontextprotocol/server"; import input from "./input.json"; export default createMcpHandler(()=>{const s=new McpServer({name:"test",version:"1"}); s.registerTool("read",{inputSchema:fromJsonSchema(input)},()=>{throw new Error("must not call tools");}); return s;});');
  writeFileSync(join(app, "empty.ts"), 'import {McpServer,createMcpHandler} from "@modelcontextprotocol/server"; export default createMcpHandler(()=>new McpServer({name:"empty",version:"1"}));');
  const config = join(root, "extract.json");
  const output = join(root, "output.json");
  writeFileSync(config, JSON.stringify({ apps: [{ name: "test", path: app, entry: "server.ts" }, { name: "empty", path: app, entry: "empty.ts" }] }));
  const result = run("extract", config, [output]);
  assert.equal(result.status, 0, result.stderr);
  const extracted = JSON.parse(readFileSync(output, "utf8"));
  assert.deepEqual(extracted.test.tools, [{name: "read", inputSchema}]);
  assert.deepEqual(extracted.empty.tools, []);
  assert.deepEqual(Object.keys(extracted.test.sources).sort(), ["input.json", "server.ts"]);

  writeFileSync(config, JSON.stringify({ root: app, sources: extracted.test.sources }));
  const verified = run("verify", config);
  assert.equal(verified.status, 0, verified.stderr);
  writeFileSync(join(app, "input.json"), JSON.stringify({type: "object"}));
  for (const mode of ["check", "verify"]) {
    const stale = run(mode, config);
    assert.equal(stale.status, 1, stale.stderr);
    assert.match(stale.stderr, /input\.json:.*refusing to publish stale types/);
  }
});

test("check loads bundled declarations and honors the application's project configuration", t => {
  const { root, run } = unpackTooling(t);
  const app = join(root, "application");
  mkdirSync(app);
  writeFileSync(join(app, "package.json"), '{"type":"module"}');
  const source = "export const total: number = [1, 2].length;\nexport const label = total.toFixed();\n";
  writeFileSync(join(app, "check.ts"), source);
  const config = join(root, "check.json");
  writeFileSync(config, JSON.stringify({ root: app, entry: "check.ts" }));
  const valid = run("check", config);
  assert.equal(valid.status, 0, valid.stderr + valid.stdout);
  writeFileSync(join(app, "check.ts"), source + "export const wrong: string = total;\n");
  const invalid = run("check", config);
  assert.equal(invalid.status, 1, invalid.stderr + invalid.stdout);
  assert.match(invalid.stderr, /check\.ts.*error/);

  writeFileSync(join(app, "check.ts"), source);
  mkdirSync(join(app, "node_modules"));
  symlinkSync(fileURLToPath(new URL("../node_modules/typescript", import.meta.url)), join(app, "node_modules/typescript"));
  writeFileSync(join(app, "tsconfig.json"), JSON.stringify({ compilerOptions: { target: "ES2023", module: "ESNext", moduleResolution: "Bundler", strict: true }, files: ["check.ts"] }));
  // The application's file selection takes precedence over entry/discovery.
  writeFileSync(join(app, "excluded.ts"), 'export const wrong: number = "excluded";');
  writeFileSync(config, JSON.stringify({ root: app, entry: "excluded.ts" }));
  const configured = run("check", config);
  assert.equal(configured.status, 0, configured.stderr + configured.stdout);
});
