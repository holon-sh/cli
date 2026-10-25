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

  server.registerResource(
    ...appView("Checkout", "ui://checkout/index.html", new URL("./dist/index.html", import.meta.url)),
  );

  return server;
}

export default createMcpHandler(createServer);
