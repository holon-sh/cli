# holon

`holon` generates typed clients for MCP servers. This allows invoking an MCP server as a function call your type checker understands.

This is currently supported via the [Python SDK](https://github.com/holon-sh/sdk-py), [TypeScript SDK](https://github.com/holon-sh/sdk-ts), and [UI SDK](https://github.com/holon-sh/sdk-ui).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/holon-sh/cli/main/install.sh | sh
```

## Tutorial

[`examples/`](examples) contains a Python `orders` MCP and a TypeScript `checkout` MCP that calls it and renders a UI.

```sh
git clone https://github.com/holon-sh/cli && cd cli/examples
(cd orders && uv sync)
(cd checkout && npm install)
```

`orders` needs nothing from these packages. Its type hints are its contract, and the CLI reads them from source without running the server.

`examples/orders/server.py`:

```python
from mcp.server import MCPServer
from pydantic import BaseModel


class Order(BaseModel):
    id: str
    total: float


server = MCPServer("orders")


@server.tool()
def lookup(id: str) -> Order:
    """Look up an order by id."""
    ...
```

### 1. Generate the client

List the caller and the provider directory. `checkout`'s source refers to `apps.orders`, and a listed directory whose `server.json` is named `.../orders` satisfies that, so nothing has to be declared.

```sh
cd checkout
holon types generate . ../orders
```

The CLI finds each server's entry file by convention (`server.py` or `server.ts`, at the root or in `src/`), reads its tools, and writes the client into `node_modules/holon-sh/generated/` (TypeScript) or `holon/apps/__init__.py` (Python). It adds the runtime to the project if it is missing. `orders` uses no client, so it is left untouched.

### 2. Call the tool

`examples/checkout/server.ts`:

```typescript
import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { apps } from "holon-sh";
import { z } from "zod";

function createServer() {
  const server = new McpServer({ name: "checkout", version: "1.0.0" });
  server.registerTool(
    "total",
    { inputSchema: z.object({ id: z.string() }), outputSchema: z.object({ total: z.number() }) },
    async ({ id }) => {
      const order = await apps.orders.tools.lookup({ id });
      return { content: [{ type: "text", text: String(order.total) }], structuredContent: { total: order.total } };
    },
  );
  return server;
}

export default createMcpHandler(createServer);
```

The argument and the result of `lookup` are typed from the provider's schemas. A misspelled field is a type error; a tool error throws `ToolCallError`. The same call from Python is `await apps.orders.tools.lookup({"id": id})`; see the [Python SDK](https://github.com/holon-sh/sdk-py).

### 3. Run it

The client needs the provider's URL. Set the variable named after the dependency:

```sh
(cd ../orders && uv run python server.py) &
ORDERS_MCP_URL=http://127.0.0.1:3001/mcp npm run serve
```

Authentication, custom HTTP clients and test fixtures are explicit bindings; see the runtime docs.

### 4. Add a view

`checkout` also ships a browser view, [`examples/checkout/ui/main.ts`](examples/checkout/ui/main.ts), that calls its own `total` tool through the MCP Apps host. Because `checkout`'s `package.json` declares `holon-ui`, the same `holon types generate` run writes a browser client into `node_modules/holon-ui/generated/`:

```typescript
import { App } from "@modelcontextprotocol/ext-apps";
import { createApps } from "holon-ui";

const view = new App({ name: "checkout-view", version: "1.0.0" });
await view.connect();

const apps = createApps({ checkout: { host: view } });
const { total } = await apps.checkout.tools.total({ id: "A-1" });
```

`npm run build` bundles the view into one HTML file, which `server.ts` serves as an MCP App resource with `appView`. See the [UI SDK](https://github.com/holon-sh/sdk-ui).

### 5. Catch breaking changes in CI

```sh
holon types check . ../orders
```

This regenerates the clients into a temporary copy of each app and runs the language's type checker (mypy or `tsc`) there. If `orders` renames `total`, `checkout` fails here, before anything is deployed. `holon check` is the same command.

## Call a server you don't own

A dependency that is not a listed directory is declared in the caller's `server.json`, under the `sh.holon` metadata, by its [MCP Registry](https://registry.modelcontextprotocol.io) name and version. The key is the name you use in code:

```json
{
  "name": "io.github.holon-sh/checkout",
  "version": "1.0.0",
  "description": "Check out orders",
  "_meta": {
    "io.modelcontextprotocol.registry/publisher-provided": {
      "sh.holon": {
        "requires": {
          "search": { "name": "io.github.acme/search", "version": "2.1.0" }
        }
      }
    }
  }
}
```

The CLI takes the contract from the first source the registry record offers: a published contract document (below), a contract stored beside the server's container image, or a live `tools/list` against the server. If none is available, generation still succeeds with an untyped client for that dependency and a note on stderr. A `requires` entry also pins a listed directory to an exact version, when you want that.

## Publish your server's contract

Callers outside your repository cannot read your source. Give them one language-neutral JSON document instead:

```sh
holon types generate --contract > contract.json
```

Host it at a stable HTTPS URL and point to it from your `server.json` before publishing to the registry:

```json
"sh.holon": { "types": "https://example.com/orders/1.0.0/contract.json" }
```

Callers keep declaring only your name and version. Republish the document when a tool's schema changes, and bump the version when the change is breaking.

## Commands

| Command | What it does |
|---|---|
| `holon types generate [DIR...]` | Generate typed clients for each listed app (default: the current directory). |
| `holon types generate --contract [DIR]` | Print the app's contract document to stdout; write nothing. |
| `holon types check [DIR...]` | Regenerate into temporary copies and type-check. `holon check` is an alias. |

Flags for `generate`: `--no-install` prints the command that would add a missing runtime instead of running it; `--quiet` silences progress.

Generated files start with a marker line and are rewritten on every run; do not edit them. Commit them or regenerate in CI, whichever suits your project.

## License

MIT
