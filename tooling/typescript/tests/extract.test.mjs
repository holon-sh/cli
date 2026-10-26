import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, rmSync, existsSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL, fileURLToPath } from "node:url";
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
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
  server.registerTool("read", { inputSchema: input, outputSchema: z.object({ id: z.string() }) }, async (args) => generate(args));
  return server;
}
export default createMcpHandler(createServer);
`, {
    "schemas.ts": `import { z } from "zod/v4"; export const input = z.object({ id: z.string() });`,
    "model.ts": `import { env, pipeline } from "@huggingface/transformers"; env.localModelPath = ""; export async function generate(input) { return pipeline(input); }`,
  });
  const result = await extract(root, "server.ts");
  assert.equal(result.diagnostic, undefined);
  assert.deepEqual(result.tools.map(tool => tool.name), ["read"]);
  assert.equal(existsSync(join(root, "executed")), false);
});

for (const [name, registration, diagnostic] of [
  ["conditional registration", `if (process.env.FLAG) server.registerTool("x", {}, () => {});`, /Conditional or repeated registration/],
  ["dynamic tool name", `server.registerTool(process.env.NAME, {}, () => {});`, /Cannot statically resolve process/],
  ["schema callback", `server.registerTool("x", {inputSchema:z.object({x:z.string().transform(() => { throw new Error("executed"); })})}, () => {});`, /Schema method transform/],
  ["runtime schema factory", `server.registerTool("x", {inputSchema:makeSchema()}, () => {});`, /Cannot statically resolve makeSchema/],
  ["conditional helper", `function add() { server.registerTool("x", {}, () => {}); } if (process.env.FLAG) add();`, /Conditional or repeated registration/],
  ["server escape", `installUnknownTools(server);`, /Unresolved registration helper/],
  ["deferred registration", `setTimeout(() => server.registerTool("x", {}, () => {}), 1);`, /Unresolved registration helper/],
  ["schema mutation", `const schema=z.object({}); Object.assign(schema, {}); server.registerTool("x", {inputSchema:schema}, () => {});`, /Schema data escapes static analysis/],
  ["mutable binding", `let schema=z.object({}); server.registerTool("x", {inputSchema:schema}, () => {});`, /immutable constant bindings/],
  ["conditional schema mutation", `const schema=z.object({}); if (process.env.FLAG) schema.description="changed"; server.registerTool("x", {inputSchema:schema}, () => {});`, /Conditional schema or catalog mutation/],
  ["conditional fixed catalog", `server.server.setRequestHandler("tools/list", () => { if (process.env.FLAG) return {tools:[{name:"hidden",inputSchema:{type:"object"}}]}; return {tools:[]}; });`, /Conditional or repeated registration/],
  ["throwing factory", `throw new Error("executed factory");`, /throwing factory/],
  ["destructured server method", `const {registerTool}=server; registerTool("x", {}, () => {});`, /Destructured server methods/],
  ["registration in a prompt callback", `server.registerPrompt("prompt", {}, () => {server.registerTool("hidden", {}, () => {});});`, /Resource and prompt handlers must not change the tool catalog/],
  ["mutation in a tool callback", `const schema=z.object({}); server.registerTool("x",{inputSchema:schema},()=>{schema.description="changed";return {content:[]};});`, /Deferred schema or catalog mutation/],
  ["deferred schema escape", `const schema=z.object({}); setTimeout(()=>mutate(schema),1); server.registerTool("x",{inputSchema:schema},()=>({content:[]}));`, /Schema or catalog data escapes through a deferred callback/],
  ["schema escape through an external module", `env.schema = input; server.registerTool("x",{inputSchema:input},()=>({content:[]}));`, /Mutation in server registration code/],
  ["unawaited registration", `async function register(s) {await fetch("https://example.com"); s.registerTool("late",{},()=>({content:[]}));} register(server);`, /Asynchronous registration helpers must be awaited/],
  ["non-JSON typed resource", `server.registerResource("r", "chat://r", {mimeType:"text/plain", _meta:{"sh.holon/outputSchema":{type:"object"}}}, () => ({contents:[]}));`, /must declare the application\/json mimeType/],
  ["malformed fixed schema", `server.server.setRequestHandler("tools/list", () => ({tools:[{name:"x",inputSchema:{type:"object",required:"id"}}]}));`, /Unsupported JSON Schema in x/],
  ["duplicate fixed names", `server.server.setRequestHandler("tools/list", () => ({tools:[{name:"x",inputSchema:{type:"object"}},{name:"x",inputSchema:{type:"object"}}]}));`, /Tool names must be unique/],
]) {
  test(`rejects ${name} without returning a partial catalog`, async t => {
    const root = fixture(t, `import { McpServer,createMcpHandler } from "@modelcontextprotocol/server"; import {z} from "zod"; import {env} from "runtime-library"; const input=z.object({}); export default createMcpHandler(() => { const server=new McpServer({name:"x",version:"1"}); ${registration} return server; });`);
    const result = await extract(root, "server.ts");
    assert.equal(result.tools, undefined);
    assert.match(result.diagnostic, diagnostic);
  });
}

test("extracts imported registrations and typed resources from the application catalog", async t => {
  const root = fixture(t, `import {createMcpHandler,McpServer} from "@modelcontextprotocol/server";
import {register} from "./helper.js";
export default createMcpHandler(() => {
  const server=new McpServer({name:"fixture",version:"1"});
  register(server);
  server.registerTool("no_output", {}, () => ({content:[]}));
  server.registerResource("snapshot", "fixture://snapshot", {mimeType:"application/json", _meta:{"sh.holon/outputSchema":{type:"number"}}}, () => ({contents:[]}));
  server.registerResource("readme", "fixture://readme", {mimeType:"text/markdown"}, () => ({contents:[]}));
  return server;
}, {legacy:"reject"});`, {
    "package.json": '{"type":"module"}',
    "helper.ts": `import {z} from "zod"; export function register(server) { server.registerTool("read", {inputSchema:z.object({id:z.string(),count:z.number().default(5)}),outputSchema:z.number()}, () => ({content:[]})); }`,
  });
  const extracted = await extract(root, "server.ts");
  assert.equal(extracted.diagnostic, undefined);
  symlinkSync(fileURLToPath(new URL("../node_modules", import.meta.url)), join(root, "node_modules"));
  symlinkSync("helper.ts", join(root, "helper.js"));
  const { default: handler } = await import(pathToFileURL(join(root, "server.ts")));
  const client = new Client({name:"test",version:"1"}, {versionNegotiation:{mode:"auto"}});
  try {
    await client.connect(new StreamableHTTPClientTransport(new URL("http://fixture/mcp"), {fetch: (url, init) => handler.fetch(new Request(url, init))}));
    const actual = await client.listTools();
    assert.deepEqual(extracted.tools, actual.tools.map(({name,inputSchema,outputSchema}) => ({name,inputSchema,...(outputSchema ? {outputSchema} : {})})).sort((a,b) => a.name.localeCompare(b.name)));
    assert.deepEqual(extracted.resources, [{uri:"fixture://snapshot", outputSchema:{type:"number"}}]);
  } finally { await Promise.all([client.close(), handler.close()]); }
});

test("follows exported handlers and rejects side-effect schema mutation", async t => {
  const root = fixture(t, `export {default} from "./handler.js";`, {
    "handler.ts": `import {createMcpHandler} from "@modelcontextprotocol/server"; import {server} from "./registered.js"; const factory = () => server; const handler = createMcpHandler(factory); export default handler;`,
    "registered.ts": `import {McpServer} from "@modelcontextprotocol/server"; import {z} from "zod"; export const server=new McpServer({name:"x",version:"1"}); server.registerTool("read",{inputSchema:z.object({id:z.string()})},()=>({content:[]}));`,
  });
  const result = await extract(root, "server.ts");
  assert.equal(result.diagnostic, undefined);
  assert.deepEqual(result.tools.map(tool => tool.name), ["read"]);
  writeFileSync(join(root,"server.ts"), `import "./mutate.js"; export {default} from "./handler.js";`);
  writeFileSync(join(root,"mutate.ts"), `import {server} from "./registered.js"; server.removeTool("read");`);
  assert.match((await extract(root,"server.ts")).diagnostic, /mutate\.ts.*Server mutation removeTool is unsupported/);
});

test("uses constant JSON Schema rather than its independent generic and follows aliases and spreads", async t => {
  for (const registration of [
    'const registered = alias["registerTool"](toolName, {...config}, () => {});',
    'alias.server.setRequestHandler("tools/list", () => ({tools:[{name:toolName,inputSchema:contract}]}));',
  ]) {
    const root = fixture(t, `import { McpServer as Server, createMcpHandler as handler, fromJsonSchema } from "@modelcontextprotocol/server";
import { contract, toolName } from "./schemas.js";
const config = {inputSchema:fromJsonSchema<{id:number}>(contract)};
export default handler(() => {const s = new Server({name:"x",version:"1"}); const alias = s; ${registration} return s; });
`, { "schemas.ts": `export const toolName="get-order"; const properties={id:{type:"string"}}; export const contract={type:"object",properties:{...properties},required:["id"],additionalProperties:false};` });
    const result = await extract(root, "server.ts");
    assert.equal(result.diagnostic, undefined);
    assert.deepEqual(result.tools, [{
      name: "get-order",
      inputSchema: {type: "object", properties: {id: {type: "string"}}, required: ["id"], additionalProperties: false},
    }]);
  }
});
