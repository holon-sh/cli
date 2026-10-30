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
  const run = (mode, config, output = []) => execFileSync(process.execPath, [join(tooling, "runner.mjs"), mode, config, ...output], { encoding: "utf8", stdio: "pipe", timeout: 30000 });
  return { root, run };
}

test("the standalone runner extracts a batch without installed dependencies and rejects stale contracts", t => {
  const { root, run } = unpackTooling(t);
  const app = join(root, "application");
  mkdirSync(app);
  const inputSchema = {type: "object", properties: {id: {type: "string"}}, required: ["id"]};
  writeFileSync(join(app, "input.json"), JSON.stringify(inputSchema));
  writeFileSync(join(app, "server.ts"), 'import {McpServer,createMcpHandler,fromJsonSchema} from "@modelcontextprotocol/server"; import input from "./input.json"; export default createMcpHandler(()=>{const s=new McpServer({name:"test",version:"1"}); s.registerTool("read",{inputSchema:fromJsonSchema(input)},()=>{}); return s;});');
  writeFileSync(join(app, "empty.ts"), 'import {McpServer,createMcpHandler} from "@modelcontextprotocol/server"; export default createMcpHandler(()=>new McpServer({name:"empty",version:"1"}));');
  // Awaiting a function's result needs the standard library embedded beside the bundled compiler.
  writeFileSync(join(app, "function.ts"), 'import {McpServer,createMcpHandler} from "@modelcontextprotocol/server"; import {register,tool} from "holon-sh/app"; const ping=tool({},async()=>({ok:true})); export default createMcpHandler(()=>{const s=new McpServer({name:"function",version:"1"}); register(s,{tools:{ping}}); return s;});');
  const config = join(root, "extract.json");
  const output = join(root, "output.json");
  writeFileSync(config, JSON.stringify({ apps: [{ name: "test", path: app, entry: "server.ts" }, { name: "empty", path: app, entry: "empty.ts" }, { name: "function", path: app, entry: "function.ts" }] }));
  run("extract", config, [output]);
  const extracted = JSON.parse(readFileSync(output, "utf8"));
  assert.deepEqual(extracted.test.tools, [{name: "read", inputSchema}]);
  assert.deepEqual(extracted.empty.tools, []);
  assert.deepEqual(extracted.function.tools[0].outputSchema, {type: "object", properties: {ok: {type: "boolean"}}, required: ["ok"], additionalProperties: false});

  writeFileSync(config, JSON.stringify({ root: app, sources: extracted.test.sources }));
  run("verify", config);
  writeFileSync(join(app, "input.json"), JSON.stringify({type: "object"}));
  for (const mode of ["check", "verify"]) {
    assert.throws(() => run(mode, config), { stderr: /input\.json:.*stale/ });
  }
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
