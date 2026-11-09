import { readFile } from "node:fs/promises";
import { RESOURCE_MIME_TYPE } from "@modelcontextprotocol/ext-apps/server";
import { createMcpHandler, McpServer } from "@modelcontextprotocol/server";
import { z } from "zod";

// Holon serves the page built into dist/web at /{{.App}}/.
export const frontend = new URL("./dist/web/", import.meta.url);
const view = "ui://{{.App}}/view.html";

function createServer() {
  const server = new McpServer({ name: "{{.Name}}", version: "0.1.0", description: "The {{.App}} app" });
  server.registerTool(
    "greet",
    {
      description: "Greet someone by name.",
      inputSchema: z.strictObject({ name: z.string() }),
      outputSchema: z.strictObject({ result: z.string() }),
      _meta: { ui: { resourceUri: view } },
    },
    ({ name }) => {
      const output = { result: `Hello, ${name}!` };
      return { content: [{ type: "text", text: output.result }], structuredContent: output };
    },
  );
  server.registerResource("View", view, { mimeType: RESOURCE_MIME_TYPE }, async () => ({
    contents: [
      {
        uri: view,
        mimeType: RESOURCE_MIME_TYPE,
        text: await readFile(new URL("./dist/mcp/index.html", import.meta.url), "utf8"),
      },
    ],
  }));
  return server;
}

export default createMcpHandler(createServer, { legacy: "reject" });
