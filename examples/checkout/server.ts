import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { apps } from "holon-sh";
import { appView } from "holon-sh/view";
import { z } from "zod";

function createServer() {
  const server = new McpServer({ name: "checkout", version: "1.0.0" });

  server.registerTool(
    "total",
    {
      description: "Total of an order, looked up from the orders server",
      inputSchema: z.object({ id: z.string() }),
      outputSchema: z.object({ total: z.number() }),
    },
    async ({ id }) => {
      const order = await apps.orders.tools.lookup({ id });
      return { content: [{ type: "text", text: String(order.total) }], structuredContent: { total: order.total } };
    },
  );

  server.registerTool(
    "docs",
    {
      description: "Find documentation for a library, through the public Context7 MCP server",
      inputSchema: z.object({ library: z.string(), query: z.string() }),
    },
    async ({ library, query }) => {
      const result = await apps.context7.callTool({
        name: "resolve-library-id",
        arguments: { libraryName: library, query },
      });
      if (result.resultType !== "complete") throw new Error("Context7 asked for more input");
      return { content: result.content, isError: result.isError };
    },
  );

  server.registerResource(
    ...appView("Checkout", "ui://checkout/index.html", new URL("./dist/index.html", import.meta.url)),
  );

  return server;
}

export default createMcpHandler(createServer);
