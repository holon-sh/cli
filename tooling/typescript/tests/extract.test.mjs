import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, rmSync, existsSync, symlinkSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { extract } from "../extract.mjs";

function fixture(t, source, files = {}) {
  const root = mkdtempSync(join(tmpdir(), "holon-contract-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  for (const [name, value] of Object.entries({ "server.ts": source, ...files })) writeFileSync(join(root, name), value);
  return root;
}

test("extracts wire schemas without executing application initialization or schema callbacks", t => {
  const root = fixture(t, `
import { createMcpHandler, McpServer } from "@modelcontextprotocol/server";
import { input } from "./schemas.js";
import { generate } from "./model.js";
import { z } from "zod";
import { writeFileSync } from "node:fs";
writeFileSync(new URL("./executed", import.meta.url), "yes");
fetch("http://127.0.0.1:1/must-not-run");
setInterval(() => { throw new Error("executed timer"); }, 1);
function createServer() {
  const config = connectToDatabase();
  const server = new McpServer({ name: "test", version: "1" });
  server.registerTool("read", { inputSchema: input, outputSchema: z.object({ id: z.string() }) }, async (args) => generate(args));
  return server;
}
export default createMcpHandler(createServer);
`, {
    "schemas.ts": `import { z } from "zod/v4"; export const input = z.object({ id: z.string(), limit: z.number().int().min(1).default(5) });`,
    "model.ts": `import { env, pipeline } from "@huggingface/transformers"; env.allowRemoteModels = false; env.useFSCache = false; env.localModelPath = ""; export async function generate(input) { return pipeline(input); }`,
  });
  const result = extract(root, "server.ts");
  assert.equal(result.diagnostic, undefined);
  assert.equal(result.tools[0].name, "read");
  assert.equal(existsSync(join(root, "executed")), false);
});

test("recognizes imported registration helpers", t => {
  const root = fixture(t, `
import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { register } from "./helper.js";
export default createMcpHandler(() => { const server = new McpServer({name:"x",version:"1"}); register(server); return server; });
`, { "helper.ts": `import { z } from "zod"; export function register(s) { s.registerTool("read", { inputSchema: z.strictObject({id:z.string()}) }, () => {}); }` });
  const result = extract(root, "server.ts");
  assert.equal(result.diagnostic, undefined);
  assert.equal(result.tools[0].name, "read");
  assert.equal(result.tools[0].inputSchema.properties.id.type, "string");
});

test("does not advertise partial catalogs or execute unsupported schemas", t => {
  for (const registration of [
    `if (process.env.FLAG) server.registerTool("x", {inputSchema:z.object({})}, () => {});`,
    `server.registerTool(process.env.NAME, {inputSchema:z.object({})}, () => {});`,
    `server.registerTool("x", {inputSchema:z.object({x:z.string().transform(() => { throw new Error("executed"); })})}, () => {});`,
    `server.registerTool("x", {inputSchema:makeSchema()}, () => {});`,
    `function add() { server.registerTool("x", {inputSchema:z.object({})}, () => {}); } if (process.env.FLAG) add();`,
    `installUnknownTools(server);`,
    `setTimeout(() => server.registerTool("x", {inputSchema:z.object({})}, () => {}), 1);`,
    `const schema=z.object({}); Object.assign(schema, {}); server.registerTool("x", {inputSchema:schema}, () => {});`,
    `let schema=z.object({}); server.registerTool("x", {inputSchema:schema}, () => {});`,
    `const schema=z.object({}); if (process.env.FLAG) schema._def.shape={}; server.registerTool("x", {inputSchema:schema}, () => {});`,
    `server.server.setRequestHandler("tools/list", () => { if (process.env.FLAG) return {tools:[{name:"hidden",inputSchema:{type:"object"}}]}; return {tools:[]}; });`,
    `throw new Error("executed factory");`,
    `const {registerTool}=server; registerTool("x", {inputSchema:z.object({})}, () => {});`,
    `server.registerPrompt("prompt", {}, () => {server.registerTool("hidden", {}, () => {});});`,
    `const schema=z.object({}); server.registerTool("x",{inputSchema:schema},()=>{schema._def.shape={};return {content:[]};});`,
    `const schema=z.object({}); setTimeout(()=>mutate(schema),1); server.registerTool("x",{inputSchema:schema},()=>({content:[]}));`,
    `env.schema = input; server.registerTool("x",{inputSchema:input},()=>({content:[]}));`,
    `async function register(s) {await fetch("https://example.com"); s.registerTool("late",{},()=>({content:[]}));} register(server);`,
    `server.registerResource("r", "chat://r", {mimeType:"text/plain", _meta:{"sh.holon/outputSchema":{type:"object"}}}, () => ({contents:[]}));`,
  ]) {
    const root = fixture(t, `import { McpServer,createMcpHandler } from "@modelcontextprotocol/server"; import {z} from "zod"; import {env} from "runtime-library"; const input=z.object({}); export default createMcpHandler(() => { const server=new McpServer({name:"x",version:"1"}); ${registration} return server; });`);
    const result = extract(root, "server.ts");
    assert.equal(result.tools, undefined);
    assert.match(result.diagnostic, /server.ts/);
    assert.doesNotMatch(result.diagnostic, /Error: executed/);
  }
});

test("extracted catalogs match the official SDK for wire defaults, schema shapes and fixed catalogs", async t => {
  for (const registration of [
    `server.registerTool("read", {inputSchema: input, outputSchema: output}, () => ({content:[]})); server.registerTool("no_output", {}, () => ({content:[]})); server.registerResource("snapshot", "fixture://snapshot", {mimeType:"application/json", _meta:{"sh.holon/outputSchema":z.toJSONSchema(output)}}, () => ({contents:[]})); server.registerResource("readme", "fixture://readme", {mimeType:"text/markdown"}, () => ({contents:[]}));`,
    `server.registerTool("variant", {inputSchema:z.discriminatedUnion("kind",[z.object({kind:z.literal("a")}),z.object({kind:z.literal("b")})]),outputSchema:z.union([output,z.object({error:z.string()})])}, () => ({content:[]})); server.registerTool("primitive_output",{inputSchema:input,outputSchema:z.number()},()=>({content:[]}));`,
    `server.server.setRequestHandler("tools/list", () => ({tools:[{name:"read",inputSchema:z.toJSONSchema(input,{io:"input"}),outputSchema:z.toJSONSchema(output)}]}));`,
  ]) {
    const root = fixture(t, `import {createMcpHandler,McpServer} from "@modelcontextprotocol/server";
import {z} from "zod"; import {input,output} from "./schemas.js";
export default createMcpHandler(() => {const server=new McpServer({name:"fixture",version:"1"},{capabilities:{tools:{},resources:{}}}); ${registration} return server;}, {legacy:"reject"});`, {
      "schemas.ts": `import {z} from "zod";
export const input=z.strictObject({id:z.string().min(1),count:z.number().int().min(1).default(5),choice:z.enum(["a","b"]).optional(),tuple:z.tuple([z.string(),z.number()]).optional(),list:z.array(z.boolean()).optional(),map:z.record(z.string(),z.number()).optional(),union:z.union([z.string(),z.number()]).nullable().optional(),both:z.intersection(z.object({a:z.string()}),z.object({b:z.boolean()})).optional(),literal:z.literal("fixed").optional(),loose:z.looseObject({enabled:z.boolean()}).optional()});
export const output=z.object({id:z.string(),value:z.unknown().optional()});`,
      "package.json": '{"type":"module"}',
    });
    const extracted = extract(root, "server.ts");
    assert.equal(extracted.diagnostic, undefined);
    symlinkSync(resolve("node_modules"), join(root, "node_modules"));
    symlinkSync("schemas.ts", join(root, "schemas.js"));
    const { default: handler } = await import(pathToFileURL(join(root, "server.ts")));
    const client = new Client({name:"test",version:"1"}, {versionNegotiation:{mode:"auto"}});
    try {
      await client.connect(new StreamableHTTPClientTransport(new URL("http://fixture/mcp"), {fetch: (url, init) => handler.fetch(new Request(url, init))}));
      const actual = await client.listTools();
      assert.deepEqual(extracted.tools, actual.tools.map(({name,inputSchema,outputSchema}) => ({name,inputSchema,...(outputSchema ? {outputSchema} : {})})).sort((a,b) => a.name.localeCompare(b.name)));
      const resources = await client.listResources();
      assert.deepEqual(extracted.resources, resources.resources.filter(r => r._meta?.["sh.holon/outputSchema"]).map(r => ({uri: r.uri, outputSchema: r._meta["sh.holon/outputSchema"]})));
    } finally { await client.close(); await handler.close(); }
  }
});

test("follows exported handlers and rejects side-effect schema mutation", t => {
  const root = fixture(t, `export {default} from "./handler.js";`, {
    "handler.ts": `import {createMcpHandler} from "@modelcontextprotocol/server"; import {server} from "./registered.js"; const factory = () => server; const handler = createMcpHandler(factory); export default handler;`,
    "registered.ts": `import {McpServer} from "@modelcontextprotocol/server"; import {z} from "zod"; export const server=new McpServer({name:"x",version:"1"}); server.registerTool("read",{inputSchema:z.object({id:z.string()})},()=>({content:[]}));`,
  });
  assert.equal(extract(root, "server.ts").tools?.[0].name, "read");
  writeFileSync(join(root,"server.ts"), `import "./mutate.js"; export {default} from "./handler.js";`);
  writeFileSync(join(root,"mutate.ts"), `import {server} from "./registered.js"; server.removeTool("read");`);
  assert.match(extract(root,"server.ts").diagnostic, /mutate.ts.*unsupported/);
});

test("uses constant JSON Schema rather than its independent generic and follows aliases and spreads", t => {
  const root = fixture(t, `import { McpServer as Server, createMcpHandler as handler, fromJsonSchema } from "@modelcontextprotocol/server";
import { contract, toolName } from "./schemas.js";
const config = {inputSchema:fromJsonSchema<{id:number}>(contract)};
export default handler(() => {const s = new Server({name:"x",version:"1"}); const alias = s; const registered = alias["registerTool"](toolName, {...config}, () => {}); return s; });
`, { "schemas.ts": `export const toolName="get-order"; const properties={id:{type:"string"}}; export const contract={type:"object",properties:{...properties},required:["id"],additionalProperties:false};` });
  const result = extract(root, "server.ts");
  assert.equal(result.diagnostic, undefined);
  assert.equal(result.tools[0].name, "get-order");
  assert.equal(result.tools[0].inputSchema.properties.id.type, "string");
  assert.equal(result.tools[0].outputSchema, undefined);
});

test("recognizes generated app imports", t => {
  for (const specifier of ["holon-sh", "holon-sh/browser", "@holon-sh/apps", "@holon-sh/apps/browser"]) {
    const root = fixture(t, `import {apps} from "${specifier}"; export default {};`);
    assert.equal(extract(root, "server.ts").usesApps, true, specifier);
  }
});


test("installed dependencies do not opt an application into generated apps", t => {
  const root = fixture(t, 'import {McpServer,createMcpHandler} from "@modelcontextprotocol/server"; export default createMcpHandler(()=>new McpServer({name:"x",version:"1"}));');
  mkdirSync(join(root, "node_modules/dependency"), {recursive:true});
  writeFileSync(join(root, "node_modules/dependency/index.js"), 'import {apps} from "holon-sh";');
  assert.equal(extract(root, "server.ts").usesApps, false);
});
