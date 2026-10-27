import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, rmSync, existsSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { extract } from "../extract.mjs";

function fixture(t, source, files = {}) {
  const root = mkdtempSync(join(tmpdir(), "holon-contract-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  for (const [name, value] of Object.entries({ "server.ts": source, ...files })) writeFileSync(join(root, name), value);
  return root;
}

test("extracts without executing application initialization or callbacks", async t => {
  const root = fixture(t, `
import { createMcpHandler, McpServer } from "@modelcontextprotocol/server";
import { input } from "./schemas.js";
import { generate } from "./model.js";
import { z } from "zod";
import { writeFileSync } from "node:fs";
writeFileSync(new URL("./executed", import.meta.url), "yes");
function createServer() {
  const config = connectToDatabase();
  const server = new McpServer({ name: "test", version: "1" });
  server.registerTool("read", { inputSchema: input, outputSchema: z.object({ id: z.string() }) }, async (input) => {
    writeFileSync(new URL("./executed", import.meta.url), "tool callback");
    return generate(input);
  });
  return server;
}
export default createMcpHandler(createServer);
`, {
    "schemas.ts": `import { z } from "zod/v4"; export const input = { id: z.string() };`,
    "model.ts": `import { env } from "runtime-library"; env.localModelPath = ""; export async function generate(input) { return (await import("./runtime.js")).generate(input); }`,
  });
  const result = await extract(root, "server.ts");
  assert.equal(result.diagnostic, undefined);
  assert.deepEqual(result.tools.map(tool => tool.name), ["read"]);
  assert.deepEqual(result.tools[0].inputSchema.properties, {id: {type: "string"}});
  assert.equal(existsSync(join(root, "executed")), false);

  writeFileSync(join(root, "schemas.ts"), `import {z} from "zod"; import {writeFileSync} from "node:fs";
function makeSchema() { writeFileSync(new URL("./executed", import.meta.url), "schema factory"); return z.object({[process.env.FIELD]:z.string()}); }
export const input = makeSchema();`);
  const dynamic = await extract(root, "server.ts");
  assert.equal(dynamic.tools, undefined);
  assert.match(dynamic.diagnostic, /schemas\.ts.*Application functions/);
  assert.equal(existsSync(join(root, "executed")), false);
});

for (const [name, registration, reason] of [
  ["dynamic tool name", `server.registerTool(process.env.NAME, {}, () => {});`, /static.*process/i],
  ["conditional registration", `function add() { server.registerTool("x", {}, () => {}); } if (process.env.FLAG) add();`, /conditional.*registration/i],
  ["unknown registration helper", `installUnknownTools(server);`, /registration helper/i],
  ["deferred registration", `setTimeout(() => server.registerTool("x", {}, () => {}), 1);`, /registration helper/i],
  ["conditional schema mutation", `const schema={type:"object",properties:{id:{type:"string"}}}; if (process.env.FLAG) schema.properties.id.type="number"; server.registerTool("x", {inputSchema:fromJsonSchema(schema)}, () => {});`, /schema.*mutation/i],
  ["conditional fixed catalog", `server.server.setRequestHandler("tools/list", () => { if (process.env.FLAG) return {tools:[{name:"hidden",inputSchema:{type:"object"}}]}; return {tools:[]}; });`, /conditional.*registration/i],
  ["deferred schema mutation", `const schema={type:"object"}; setTimeout(()=>mutate(schema),1); server.registerTool("x",{inputSchema:fromJsonSchema(schema)},()=>({content:[]}));`, /schema.*deferred callback/i],
  ["unawaited registration", `async function register(s) {await fetch("https://example.com"); s.registerTool("late",{},()=>({content:[]}));} register(server);`, /awaited/i],
  ["non-JSON typed resource", `server.registerResource("r", "chat://r", {mimeType:"text/plain", _meta:{"sh.holon/outputSchema":{type:"object"}}}, () => ({contents:[]}));`, /application\/json/],
  ["malformed fixed schema", `server.server.setRequestHandler("tools/list", () => ({tools:[{name:"x",inputSchema:{type:"object",required:"id"}}]}));`, /JSON Schema/i],
]) {
  test(`rejects ${name} without returning a partial catalog`, async t => {
    const root = fixture(t, `import {McpServer,createMcpHandler,fromJsonSchema} from "@modelcontextprotocol/server"; export default createMcpHandler(() => { const server=new McpServer({name:"x",version:"1"}); ${registration} return server; });`);
    const result = await extract(root, "server.ts");
    assert.equal(result.tools, undefined);
    assert.equal(result.resources, undefined);
    // Check the source and rejection reason, not the evaluator's exact wording.
    assert.match(result.diagnostic, /server\.ts/);
    assert.match(result.diagnostic, reason);
  });
}

test("extracts imported tool declarations and selects typed resources", async t => {
  for (const registration of [
    'const registered = server.registerTool(toolName, {...config}, () => {});',
    'server.server.setRequestHandler("tools/list", () => ({tools:[{name:toolName,inputSchema:input,outputSchema:output}]}));',
  ]) {
    const root = fixture(t, `import {createMcpHandler,McpServer} from "@modelcontextprotocol/server";
import {register} from "./helper.js";
export default createMcpHandler(() => {
  const server=new McpServer({name:"fixture",version:"1"});
  register(server);
  server.registerResource("snapshot", "fixture://snapshot", {mimeType:"application/json", _meta:{"sh.holon/outputSchema":{type:"number"}}}, () => ({contents:[]}));
  server.registerResource("readme", "fixture://readme", {mimeType:"text/markdown"}, () => ({contents:[]}));
  return server;
});`, {
      "schemas.ts": 'export const toolName="get-order"; const properties={id:{type:"string"}}; export const input={type:"object",properties:{...properties},required:["id"],additionalProperties:false}; export const output={type:"number"};',
      "helper.ts": `import {fromJsonSchema} from "@modelcontextprotocol/server"; import {toolName,input,output} from "./schemas.js";
const config={inputSchema:fromJsonSchema<{id:number}>(input),outputSchema:fromJsonSchema(output)};
export function register(server) { ${registration} }`,
    });
    const result = await extract(root, "server.ts");
    assert.equal(result.diagnostic, undefined);
    // The declared wire schema wins over fromJsonSchema's independent generic.
    assert.deepEqual(result.tools, [{
      name: "get-order",
      inputSchema: {type:"object", properties:{id:{type:"string"}}, required:["id"], additionalProperties:false},
      outputSchema: {type:"number"},
    }]);
    assert.deepEqual(result.resources, [{uri:"fixture://snapshot", outputSchema:{type:"number"}}]);
  }
});

test("follows exported handlers and includes side-effect imports in catalog analysis", async t => {
  const root = fixture(t, `export {default} from "./handler.js";`, {
    "handler.ts": `import {createMcpHandler} from "@modelcontextprotocol/server"; import {server} from "./registered.js"; const factory = () => server; const handler = createMcpHandler(factory); export default handler;`,
    "registered.ts": `import {McpServer} from "@modelcontextprotocol/server"; import {z} from "zod"; export const server=new McpServer({name:"x",version:"1"}); server.registerTool("read",{inputSchema:z.object({id:z.string()})},()=>({content:[]}));`,
  });
  const result = await extract(root, "server.ts");
  assert.equal(result.diagnostic, undefined);
  assert.deepEqual(result.tools.map(tool => tool.name), ["read"]);
  writeFileSync(join(root,"server.ts"), `import "./mutate.js"; export {default} from "./handler.js";`);
  writeFileSync(join(root,"mutate.ts"), `import {server} from "./registered.js"; if (process.env.DISABLE) server.removeTool("read");`);
  const changed = await extract(root,"server.ts");
  assert.equal(changed.tools, undefined);
  assert.match(changed.diagnostic, /mutate\.ts.*conditional/i);
});

test("rejects imports that escape the application snapshot through a symlink", async t => {
  const outside = fixture(t, 'export const input={type:"object"};');
  const root = fixture(t, `import {McpServer,createMcpHandler,fromJsonSchema} from "@modelcontextprotocol/server";
import {input} from "./schema.js";
export default createMcpHandler(() => {const server=new McpServer({name:"x",version:"1"}); server.registerTool("x",{inputSchema:fromJsonSchema(input)},()=>{}); return server;});`);
  symlinkSync(join(outside, "server.ts"), join(root, "schema.ts"));
  const result = await extract(root, "server.ts");
  assert.equal(result.tools, undefined);
  assert.match(result.diagnostic, /application source/);
});
