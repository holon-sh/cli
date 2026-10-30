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

test("function app types compile to MCP without running server code", async t => {
  const root = fixture(t, `
import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { register, resource, tool } from "holon-sh/app";
import { appView } from "holon-sh/view";
import { writeFileSync } from "node:fs";
import type { Count } from "./types.js";
writeFileSync(new URL("./executed", import.meta.url), "module");
let count = 0;
const state = resource("counter://state", (): Count => ({ count }));
/** Increase the counter. */
const add = tool({ changes: [state], annotations: { destructiveHint: false } }, async ({ amount }: { amount: number }) => {
  writeFileSync(new URL("./executed", import.meta.url), "tool");
  return { count: (count += amount) };
});
const show = tool({ annotations: { readOnlyHint: true }, _meta: { ui: { resourceUri: "ui://counter/index.html" } } }, (): Count => ({ count }));
const info = { name: "example.com/counter", version: "1.0.0" };
export const publishing = { packages: [{ identifier: \`example:\${info.version}\` }] };
const handler = createMcpHandler(() => {
  const server = new McpServer(info, { capabilities: { resources: { subscribe: true } } });
  register(server, { tools: { show, add }, resources: { state } }, uri => handler.notify.resourceUpdated(uri));
  server.registerResource(...appView("Counter", "ui://counter/index.html", new URL("./dist/index.html", import.meta.url)));
  return server;
}, { legacy: "stateless" });
export default handler;
`, { "types.ts": "export interface Count {\n  /** Current total. */\n  count: number;\n}\n" });
  const result = await extract(root, "server.ts");
  assert.equal(result.diagnostic, undefined);
  const counter = { type: "object", properties: { count: { type: "number", description: "Current total." } }, required: ["count"], additionalProperties: false };
  assert.deepEqual(result.tools, [
    {
      name: "add",
      description: "Increase the counter.",
      annotations: { destructiveHint: false },
      _meta: { "sh.holon/changes": ["counter://state"] },
      inputSchema: { type: "object", properties: { amount: { type: "number" } }, required: ["amount"], additionalProperties: false },
      outputSchema: { type: "object", properties: { count: { type: "number" } }, required: ["count"], additionalProperties: false },
    },
    {
      name: "show",
      annotations: { readOnlyHint: true },
      _meta: { ui: { resourceUri: "ui://counter/index.html" } },
      inputSchema: { type: "object", properties: {}, additionalProperties: false },
      outputSchema: counter,
    },
  ]);
  // The appView registration is accepted beside the typed resource; only JSON resources are typed.
  assert.deepEqual(result.resources, [{ uri: "counter://state", outputSchema: counter }]);
  assert.deepEqual(Object.keys(result.sources).sort(), ["server.ts", "types.ts"]);
  assert.equal(existsSync(join(root, "executed")), false);
  assert.deepEqual((await extract(root, "server.ts", true)).descriptor, { name: "example.com/counter", version: "1.0.0", packages: [{ identifier: "example:1.0.0" }] });
});

test("extracts without executing application initialization or callbacks", async t => {
  const root = fixture(t, `
import { createMcpHandler, McpServer } from "@modelcontextprotocol/server";
import { z } from "zod";
import { writeFileSync } from "node:fs";
import { pages } from "./pages.js";
writeFileSync(new URL("./executed", import.meta.url), "yes");
export default createMcpHandler(() => {
  const server = new McpServer({ name: "test", version: "1" });
  server.registerTool("read", { inputSchema: { page: z.enum(Object.keys(pages)).default("home"), id: z.string().refine(() => { writeFileSync(new URL("./executed", import.meta.url), "refinement"); return true; }) } }, () => {
    writeFileSync(new URL("./executed", import.meta.url), "tool callback");
  });
  for (const name of process.env.RESOURCES) {
    server.registerResource(name, name, {mimeType:"text/plain"}, () => ({contents:[]}));
  }
  return server;
});`, {
    "pages.ts": `import { writeFileSync } from "node:fs"; const home="home"; export const pages={ [home]: writeFileSync(new URL("./executed", import.meta.url), "page value"), ...{research: () => {}}, careers: null } as const satisfies Record<string, unknown>;`,
  });
  const result = await extract(root, "server.ts");
  assert.equal(result.diagnostic, undefined);
  assert.deepEqual(result.tools.map(tool => tool.name), ["read"]);
  assert.deepEqual(result.tools[0].inputSchema.properties.id, {type:"string"});
  assert.deepEqual(result.tools[0].inputSchema.properties.page, {type:"string", enum:["home","research","careers"], default:"home"});
  assert.equal(existsSync(join(root, "executed")), false);
});

for (const [name, registration, reason] of [
  ["resource callback registration", `for (const name of process.env.RESOURCES) { server.registerResource(name,name,{mimeType:"text/plain"},()=>{server.registerTool("hidden",{},()=>{});}); }`, /conditional.*registration/i],
  ["repeated typed resource", `for (const name of process.env.RESOURCES) { server.registerResource(name,name,{mimeType:"application/json",_meta:{"sh.holon/outputSchema":{type:"number"}}},()=>({contents:[]})); }`, /conditional.*registration/i],
  ["deferred app tool", `setTimeout(() => registerAppTool(server,"late",{},()=>{}),1);`, /registration helper/i],
  ["refinement registration", `server.registerTool("x",{inputSchema:z.object({id:z.string()}).refine(()=>{server.registerTool("hidden",{},()=>{});return true;})},()=>{});`, /callbacks.*catalog/i],
  ["dynamic tool name", `server.registerTool(process.env.NAME, {}, () => {});`, /static.*process/i],
  ["shadowed Object", `const Object={keys:()=>["home"]}; server.registerTool("x",{inputSchema:z.object({page:z.enum(Object.keys({home:null}))})},()=>{});`, /functions are not executed/i],
  ["runtime object keys", `server.registerTool("x",{inputSchema:z.object({page:z.enum(Object.keys(()=>{}))})},()=>{});`, /statically declared object/i],
  ["extra Object.keys arguments", `server.registerTool("x",{inputSchema:z.object({page:z.enum(Object.keys({home:null},sideEffect()))})},()=>{});`, /one.*argument/i],
  ["overridden Object.keys", `Object.keys=()=>["other"]; server.registerTool("x",{inputSchema:z.object({page:z.enum(Object.keys({home:null}))})},()=>{});`, /mutation/i],
  ["conditional registration", `function add() { server.registerTool("x", {}, () => {}); } if (process.env.FLAG) add();`, /conditional.*registration/i],
  ["deferred registration", `setTimeout(() => server.registerTool("x", {}, () => {}), 1);`, /registration helper/i],
  ["conditional schema mutation", `const pages={home:null}; if (process.env.FLAG) pages.other=null; server.registerTool("x", {inputSchema:z.object({page:z.enum(Object.keys(pages))})}, () => {});`, /schema.*mutation/i],
  ["conditional fixed catalog", `server.server.setRequestHandler("tools/list", () => { if (process.env.FLAG) return {tools:[{name:"hidden",inputSchema:{type:"object"}}]}; return {tools:[]}; });`, /conditional.*registration/i],
  ["deferred schema mutation", `const schema={type:"object"}; setTimeout(()=>mutate(schema),1); server.registerTool("x",{inputSchema:fromJsonSchema(schema)},()=>({content:[]}));`, /schema.*deferred callback/i],
  ["unawaited registration", `async function register(s) {await fetch("https://example.com"); s.registerTool("late",{},()=>({content:[]}));} register(server);`, /awaited/i],
  ["non-JSON typed resource", `server.registerResource("r", "chat://r", {mimeType:"text/plain", _meta:{"sh.holon/outputSchema":{type:"object"}}}, () => ({contents:[]}));`, /application\/json/],
  ["malformed fixed schema", `server.server.setRequestHandler("tools/list", () => ({tools:[{name:"x",inputSchema:{type:"object",required:"id"}}]}));`, /JSON Schema/i],
  ["deferred function registration", `setTimeout(() => register(server, {tools:{}}), 1);`, /registration helper/i],
  ["mutable function declaration", `let x = tool({}, () => ({})); register(server, {tools:{x}});`, /immutable/i],
  ["unsupported function type", `register(server, {tools:{x: tool({}, (input: {when: Date}) => ({}))}});`, /^server\.ts:1:\d+: Unsupported JSON type/],
]) {
  test(`rejects ${name} without returning a partial catalog`, async t => {
    const root = fixture(t, `import {McpServer,createMcpHandler,fromJsonSchema} from "@modelcontextprotocol/server"; import {registerAppTool} from "@modelcontextprotocol/ext-apps/server"; import {register,tool} from "holon-sh/app"; import {z} from "zod"; export default createMcpHandler(() => { const server=new McpServer({name:"x",version:"1"}); ${registration} return server; });`);
    const result = await extract(root, "server.ts");
    assert.equal(result.tools, undefined);
    assert.equal(result.resources, undefined);
    assert.match(result.diagnostic, reason);
  });
}

test("extracts imported tool declarations and selects typed resources", async t => {
  for (const registration of [
    'const registered = appTool(server, toolName, {...config}, () => {});',
    'apps.registerAppTool(server, toolName, {...config}, () => {});',
    'const registered = server.registerTool(toolName, {...config}, () => {});',
    'server.server.setRequestHandler("tools/list", () => ({tools:[{name:toolName,inputSchema:input,outputSchema:output}]}));',
  ]) {
    const root = fixture(t, `import {createMcpHandler,McpServer} from "@modelcontextprotocol/server";
import {register} from "./helper.js";
import {registerAppResource, RESOURCE_MIME_TYPE} from "@modelcontextprotocol/ext-apps/server";
export default createMcpHandler(() => {
  const server=new McpServer({name:"fixture",version:"1"});
  register(server);
  server.registerResource("snapshot", "fixture://snapshot", {mimeType:"application/json", _meta:{"sh.holon/outputSchema":{type:"number"}}}, () => ({contents:[]}));
  registerAppResource(server,"app","ui://app",{mimeType:RESOURCE_MIME_TYPE},()=>({contents:[]}));
  server.registerResource("readme", "fixture://readme", {mimeType:"text/markdown"}, () => ({contents:[]}));
  return server;
});`, {
      "schemas.ts": 'export const toolName="get-order"; const properties={id:{type:"string"}}; export const input={type:"object",properties:{...properties},required:["id"],additionalProperties:false}; export const output={type:"number"};',
      "helper.ts": `import {registerAppTool as appTool} from "@modelcontextprotocol/ext-apps/server"; import * as apps from "@modelcontextprotocol/ext-apps/server"; import {fromJsonSchema} from "@modelcontextprotocol/server"; import {toolName,input,output} from "./schemas.js";
const config={inputSchema:fromJsonSchema<{id:number}>(input),outputSchema:fromJsonSchema(output)};
export function register(server) { ${registration} }`,
    });
    const result = await extract(root, "server.ts");
    // The declared wire schema wins over fromJsonSchema's independent generic.
    assert.deepEqual(result.tools, [{
      name: "get-order",
      inputSchema: {type:"object", properties:{id:{type:"string"}}, required:["id"], additionalProperties:false},
      outputSchema: {type:"number"},
    }]);
    assert.deepEqual(result.resources, [{uri:"fixture://snapshot", outputSchema:{type:"number"}}]);
  }
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
