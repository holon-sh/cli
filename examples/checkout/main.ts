import { createServer } from "node:http";
import { toNodeHandler } from "@modelcontextprotocol/node";
import handler from "./server.ts";

createServer(toNodeHandler(handler)).listen(3002, () => console.log("checkout listening on http://127.0.0.1:3002/mcp"));
