import { createMcpHandler, McpServer } from "@modelcontextprotocol/server";
import { createPage } from "holon-sh/page";
import { z } from "zod";

export const frontend = new URL("./dist/web/", import.meta.url);
const page = createPage(new URL("./dist/mcp/index.html", import.meta.url));

function createServer() {
  const server = new McpServer({ name: "{{.Name}}", version: "0.1.0", description: "The {{.App}} app" });
  server.registerTool(
    "greet",
    {
      description: "Greet someone by name.",
      inputSchema: z.strictObject({ name: z.string() }),
      outputSchema: z.strictObject({ result: z.string() }),
      _meta: page.meta,
    },
    ({ name }) => {
      const output = { result: `Hello, ${name}!` };
      return { content: [{ type: "text", text: output.result }], structuredContent: output };
    },
  );
  page.serve(server);
  return server;
}

export default createMcpHandler(createServer);
