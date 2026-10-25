# holon-types

Typed clients for MCP servers that call other MCP servers. MCP already describes every tool with a JSON Schema for its input and output; the `holon` CLI turns those schemas into a typed client module in your server's project, and type-checks your code against them. At runtime the client validates arguments before each call and results after it.

Two packages are involved:

| Package | Role |
|---|---|
| `holon-types` (PyPI) | The `holon` CLI. A static binary per platform; it generates clients for Python and TypeScript projects alike and is needed only during development. |
| `holon-sh` (PyPI and npm) | The runtime the generated client imports. Python imports it as `holon`; TypeScript as `holon-sh`. |
| `holon-ui` (npm) | The runtime for browser views (MCP Apps, WebMCP), when your server ships one. See [holon-sh/sdk-ui](https://github.com/holon-sh/sdk-ui). |

Examples below come in Python and TypeScript; follow the one you use.

## Install the CLI and the runtime

### Install the CLI

Only dev releases exist so far, so allow prereleases:

```sh
uv tool install --prerelease allow holon-types
holon --version
```

Or `pip install --pre holon-types`. Supported platforms are macOS 15 or newer and Linux, on arm64 and x86-64. The CLI reads your source with your own interpreter: Python 3.11+ for Python servers, Node.js 22+ for TypeScript servers.

### Add the runtime to your server

Do this in each server that calls other servers.

#### Python

```sh
uv add --prerelease allow holon-sh
uv add --dev mypy==2.4.0
```

`holon types check` runs mypy from the app's `.venv`.

#### TypeScript

Once `holon-sh` is published to npm:

```sh
npm install holon-sh@dev
npm install --save-dev typescript
```

If you skip this step, `holon types generate` adds the runtime for you.

## Call another MCP server with types

In this section you build `checkout`, an MCP server that calls the `lookup` tool of `orders`, another MCP server in the same repository:

```
orders/      server.json, server.py or server.ts
checkout/    server.json, server.py or server.ts
```

### Write the provider

`orders` is an ordinary MCP server and needs nothing from these packages. Its type hints, or its Zod schemas, are its contract. The CLI reads them from source without running the server.

#### Python

Make each server directory a project with the MCP SDK and mypy:

```sh
uv init --bare orders && cd orders
uv add mcp && uv add --dev mypy==2.4.0
```

`orders/server.py`:

```python
from mcp.server import MCPServer
from pydantic import BaseModel


class Order(BaseModel):
    id: str
    total: float


server = MCPServer("orders")


@server.tool()
def lookup(id: str) -> Order:
    return Order(id=id, total=42.0)


if __name__ == "__main__":
    server.run("streamable-http", port=3001)
```

#### TypeScript

`orders/server.ts`:

```typescript
import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { z } from "zod";

function createServer() {
  const server = new McpServer({ name: "orders", version: "1.0.0" });
  server.registerTool(
    "lookup",
    {
      inputSchema: z.object({ id: z.string() }),
      outputSchema: z.object({ id: z.string(), total: z.number() }),
    },
    async ({ id }) => {
      const order = { id, total: 42 };
      return { content: [{ type: "text", text: JSON.stringify(order) }], structuredContent: order };
    },
  );
  return server;
}

export default createMcpHandler(createServer);
```

The entry file default-exports the handler and starts nothing, because the CLI must be able to read every tool registration without running code. Start the HTTP server from a second file.

`orders/main.ts`:

```typescript
import { createServer } from "node:http";
import { toNodeHandler } from "@modelcontextprotocol/node";
import handler from "./server.ts";

createServer(toNodeHandler(handler)).listen(3001);
```

`orders/package.json`:

```json
{
  "name": "orders",
  "private": true,
  "type": "module",
  "scripts": { "serve": "node main.ts" },
  "dependencies": {
    "@modelcontextprotocol/node": "^2.1.1",
    "@modelcontextprotocol/server": "^2.3.1",
    "zod": "^4.6.5"
  }
}
```

Name the script anything but `start`: a `start` script (or `main`) tells the CLI which file is the entry. See "Entry files" under Reference.

### Describe both servers in `server.json`

Each server has a `server.json` in the MCP Registry's format. The provider's needs only a name, a version and a description.

`orders/server.json`:

```json
{
  "name": "io.github.example/orders",
  "version": "1.0.0",
  "description": "Look up orders"
}
```

In the caller's, declare the dependency under `sh.holon.requires` in the publisher-provided metadata. The key (`orders`) is the name you use in code; the value is the provider's server name and exact version.

`checkout/server.json`:

```json
{
  "name": "io.github.example/checkout",
  "version": "1.0.0",
  "description": "Check out orders",
  "_meta": {
    "io.modelcontextprotocol.registry/publisher-provided": {
      "sh.holon": {
        "requires": {
          "orders": { "name": "io.github.example/orders", "version": "1.0.0" }
        }
      }
    }
  }
}
```

### Call the tool

#### Python

`checkout/server.py`:

```python
from mcp.server import MCPServer

from holon.apps import apps

server = MCPServer("checkout")


@server.tool()
async def total(id: str) -> float:
    order = await apps.orders.tools.lookup({"id": id})
    return order["total"]


if __name__ == "__main__":
    server.run("streamable-http", port=3002)
```

The argument is one mapping, typed as `Inputs.orders.lookup`; the result is typed as `Outputs.orders.lookup`, a `TypedDict`. Both are exported from `holon.apps`.

#### TypeScript

`checkout/server.ts`:

```typescript
import { McpServer, createMcpHandler } from "@modelcontextprotocol/server";
import { apps } from "holon-sh";
import { z } from "zod";

function createServer() {
  const server = new McpServer({ name: "checkout", version: "1.0.0" });
  server.registerTool(
    "total",
    {
      inputSchema: z.object({ id: z.string() }),
      outputSchema: z.object({ total: z.number() }),
    },
    async ({ id }) => {
      const order = await apps.orders.tools.lookup({ id });
      return { content: [{ type: "text", text: String(order.total) }], structuredContent: { total: order.total } };
    },
  );
  return server;
}

export default createMcpHandler(createServer);
```

`apps` is a named export. The argument is typed as `Inputs["orders"]["lookup"]` and the result as `Outputs["orders"]["lookup"]`; both are exported from `"holon-sh"`. Give `checkout` a `main.ts` and `package.json` like the provider's, listening on port 3002 and with `holon-sh` among its dependencies.

### Generate the client

From `checkout`, list the caller and the provider directory:

```sh
cd checkout
holon types generate . ../orders
```

```
checkout: generated python types
orders: generated python types
```

The CLI reads `orders` without running it, builds its contract, and writes the client: `checkout/holon/apps/__init__.py` for Python, `checkout/node_modules/holon-sh/generated/` for TypeScript. Directories listed together provide each other's `requires` entries by `server.json` name. Every listed directory is treated as an app, so `orders` gets its own (empty) client too, and the CLI adds the runtime to any listed app that does not depend on it yet.

### Run it

The client needs the provider's URL. Set the environment variable named after the dependency: upper-cased, `-` replaced by `_`, plus `_MCP_URL`.

#### Python

```sh
(cd orders && uv run python server.py) &
cd checkout && ORDERS_MCP_URL=http://127.0.0.1:3001/mcp uv run python server.py
```

#### TypeScript

```sh
(cd orders && npm run serve) &
cd checkout && ORDERS_MCP_URL=http://127.0.0.1:3001/mcp npm run serve
```

Every call validates the arguments against the tool's input schema, sends the request over Streamable HTTP, validates the structured result against the output schema, and returns it. A tool error raises `ToolCallError`.

### Check it

```sh
holon types check . ../orders
```

```
Typed contracts check passed.
```

`holon types check` regenerates the clients into temporary copies of the listed apps and type-checks each one: mypy for Python, `tsc` for TypeScript. If `orders` renames `total`, `checkout` fails here, before anything runs. Run it in CI. `holon check` is the same command.

## Call a server you don't own

### Declare it by registry name

A `requires` entry whose server name is not one of the directories you list is looked up in the [MCP Registry](https://registry.modelcontextprotocol.io) by name and exact version. The declaration is the same as for a local provider:

```json
"requires": {
  "search": { "name": "io.github.acme/search", "version": "2.1.0" }
}
```

Add `"registry": "https://registry.example.com"` to an entry to use another registry that serves the same API.

### Know where the contract comes from

The CLI takes the contract from the first of these that the registry record offers:

1. A contract URL in the record's `sh.holon.types` metadata (see "Publish your server's contract" below).
2. A contract stored beside the server's container image, when the record lists an OCI package.
3. A live `tools/list` against the record's Streamable HTTP remote.

When none of them yields a contract, generation still succeeds: that dependency gets an untyped client that offers only `call_tool` (Python) or `callTool` (TypeScript), and the CLI says why on stderr:

```
checkout: typed dependency search unavailable: HTTP 404
```

## Publish your server's contract

Callers outside your repository cannot read your source. Give them the contract instead: one language-neutral JSON file that Python and TypeScript callers both use.

### Print the contract

```sh
holon types generate --contract orders > contract.json
```

```json
{
  "version": 1,
  "identity": { "name": "io.github.example/orders", "version": "1.0.0" },
  "contract": { "tools": [{ "name": "lookup", "inputSchema": {}, "outputSchema": {} }] }
}
```

`--contract` reads one directory (the working directory when you give none), prints its contract to stdout, and writes nothing.

### Reference it from your registry record

Host the file at a stable HTTPS URL and add it to your `server.json` before you publish it to the registry:

```json
"_meta": {
  "io.modelcontextprotocol.registry/publisher-provided": {
    "sh.holon": { "types": "https://example.com/orders/1.0.0/contract.json" }
  }
}
```

Callers keep declaring only your name and version; the CLI follows the link. Regenerate and republish the file whenever a tool's schema changes, and release a new version when the change is breaking: callers pin exact versions.

## Keep clients up to date

- Regenerate when a provider's tool schemas change or when you edit a `requires` block. Nothing watches for this.
- Commit the generated module or not; both work. If you do not, run `holon types generate` in CI before `holon types check`.
- In TypeScript, the `holon-sh` package regenerates after `npm install` when the install directory has a `server.json` and `holon` is on `PATH`. Set `HOLON_SKIP_POSTINSTALL=1` to turn that off.
- Run `holon types check` in CI. It is the step that proves callers still compile against their providers.
- Never edit the generated files. They start with a marker line; the CLI refuses to overwrite files without it, and rewrites the rest on every run.

## Appendix: Monorepos

List every directory that takes part in the call graph, in any order:

```sh
holon types generate checkout orders
holon types check checkout orders
```

A `requires` entry whose server name matches another listed directory's `server.json` is provided by that directory, and the provider takes the requiring alias as its app name. A directory listed alone is named after the last segment of its server name. Every other `requires` entry is resolved through the registry. Each listed Python app needs mypy in its own `.venv` for `holon types check`.

The `holon-sh` postinstall regenerates only the directory you ran `npm install` in, so it resolves sibling providers through the registry. In a monorepo, run `holon types generate` with all directories after installing, or set `HOLON_SKIP_POSTINSTALL=1`.

## Appendix: Advanced: explicit bindings

Bind a dependency explicitly when the environment variable is not enough: the server needs authentication, you want a custom HTTP client, or a test should point at a fixture. `create_apps` / `createApps` from the generated module takes a mapping of dependency name to binding and returns clients for only those dependencies.

#### Python

```python
import os

import httpx2

from holon.apps import create_apps

apps = create_apps(
    {
        "orders": {
            "endpoint": "https://orders.example.com/mcp",
            "http_client": lambda: httpx2.AsyncClient(
                headers={"Authorization": f"Bearer {os.environ['ORDERS_TOKEN']}"},
            ),
        },
    }
)
```

The factory is called once per tool call, and the client is closed afterwards.

#### TypeScript

```typescript
import { createApps } from "holon-sh";

const apps = createApps({
  orders: {
    endpoint: "https://orders.example.com/mcp",
    authProvider: { token: async () => process.env.ORDERS_TOKEN },
  },
});

// An explicit binding keeps one session open; close it on shutdown.
await apps.orders.close();
```

Hosts that bind every app at startup can register a process-wide resolver instead, with `set_default_binding` (`holon.binding`) or `setDefaultBinding` (`holon-sh/runtime`); standalone apps do not need it.

## Reference

### Commands

Every `DIR` must contain a `server.json`. With no `DIR`, the command uses the working directory; it never searches parent directories.

| Command | What it does |
|---|---|
| `holon types generate [DIR...]` | Generate typed clients for each listed app. |
| `holon types generate --contract [DIR]` | Print the app's contract document to stdout instead; writes nothing. Takes at most one `DIR`. |
| `holon types generate --no-install [DIR...]` | Print the commands that would add a missing runtime instead of running them. |
| `holon types generate --quiet [DIR...]` | Suppress progress output. |
| `holon types check [DIR...]` | Regenerate into temporary copies and type-check each listed app (mypy, `tsc`). `holon check` is the same command. |
| `holon --version` | Print the CLI version. |

### Entry files

The CLI analyzes one entry file per app, and the app must have exactly one of:

- `server.py` or `src/server.py` (Python);
- `server.ts`, `src/server.ts`, `server.js` or `src/server.js`, next to a `package.json` (TypeScript and JavaScript).

When `package.json` has a `main` field or a `start` script, that names the entry instead of the file names above. A `start` script counts only in the form `node FILE`, `tsx FILE`, `ts-node FILE` or `bun FILE`.

### Generated files

| App | Dependency in `package.json` | Generated into |
|---|---|---|
| Python | | `holon/apps/__init__.py` in the app. Do not add `holon/__init__.py`: `holon` is a namespace package shared with the runtime. |
| TypeScript | `holon-sh` | `node_modules/holon-sh/generated/`, imported as `"holon-sh"` |
| TypeScript | `holon-ui` | `node_modules/holon-ui/generated/`, imported as `"holon-ui"` (browser views) |
| TypeScript | both | both directories |
| TypeScript | neither | `node_modules/holon-sh/generated/` (the CLI adds `holon-sh`) |

Each app needs its own installed copy of the runtime package; the CLI refuses to write two apps' clients into one hoisted copy.

The generated module exports:

| Python (`holon.apps`) | TypeScript (`holon-sh`, `holon-ui`) | What it is |
|---|---|---|
| `apps` | `apps` (`holon-sh` only) | One client per declared dependency, bound through `<NAME>_MCP_URL`. |
| `create_apps(bindings)` | `createApps(bindings, options?)` | Clients for explicitly bound dependencies. |
| `Inputs.<dep>.<tool>` | `Inputs["dep"]["tool"]` | Tool argument types. |
| `Outputs.<dep>.<tool>` | `Outputs["dep"]["tool"]` | Tool result types. |
| | `Resources["dep"]["uri"]` | Typed JSON resource values. |
| `ToolCallError` is in `holon.client` | `ToolCallError` | Raised for tool errors and input requests. |
| | `exposeTools`, `registerPageTools` (`holon-ui` only) | WebMCP and MCP Apps page tools. |

Your own server is `self`. Clients generated into `holon-ui` include one for the app's own server under the reserved key `self`: a view calls `createApps({ self: { host: view } })` and then `apps.self.tools.<tool>()`, typed by `Inputs["self"]`, `Outputs["self"]` and `Resources["self"]`. For that reason `self` cannot be a `requires` alias.

### `server.json` metadata

All keys live under `_meta["io.modelcontextprotocol.registry/publisher-provided"]["sh.holon"]`.

| Key | Value |
|---|---|
| `requires.<alias>` | `{ "name": ..., "version": ..., "registry"?: ... }`: a dependency's server name, exact version, and optionally a registry URL (HTTPS, or HTTP on loopback). The alias is lowercase letters, digits and `-`, starting with a letter, at most 49 characters, and not `self`. |
| `types` | HTTPS URL of this server's published contract document. |

## License

MIT
