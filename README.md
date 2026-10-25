# holon

`holon` generates typed clients for MCP servers. This allows invoking an MCP server as a function call your type checker understands.

This is currently supported via the following SDKs:

| SDK | Description |
|---|---|
| [Python](https://github.com/holon-sh/sdk-py) | `holon-sh` on PyPI. The runtime the generated Python client imports. |
| [TypeScript](https://github.com/holon-sh/sdk-ts) | `holon-sh` on npm. The runtime the generated Node.js client imports. |
| [UI](https://github.com/holon-sh/sdk-ui) | `holon-ui` on npm. The runtime for browser pages: MCP Apps views and WebMCP. |

You install the CLI once; the first `holon types generate` adds the runtime to each project.

## Install

```sh
go install github.com/holon-sh/cli/cmd/holon@latest
```

The CLI is also on PyPI as `holon-types` (`uv tool install --prerelease allow holon-types`).

## Call another MCP server with types

`checkout` is an MCP server that wants to call the `lookup` tool of `orders`, another MCP server in the same repository. `orders` needs nothing from these packages: its type hints (Python) or Zod schemas (TypeScript) are its contract, and the CLI reads them from source without running the server.

### 1. Declare the dependency

Each server has a `server.json` in the MCP Registry format. In the caller's, add `requires` under the `sh.holon` metadata. The key is the name you will use in code; the value is the provider's registry name and version.

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

### 2. Generate the client

List the caller and the provider directory. Directories listed together resolve each other's `requires` by `server.json` name.

```sh
cd checkout
holon types generate . ../orders
```

The CLI finds each server's entry file by convention (`server.py` or `server.ts`, at the root or in `src/`), reads its tools, and writes the client: `holon/apps/__init__.py` for Python, `node_modules/holon-sh/generated/` for TypeScript. It also adds the runtime to the project if it is missing.

### 3. Call the tool

Python:

```python
from mcp.server import MCPServer
from holon.apps import apps

server = MCPServer("checkout")

@server.tool()
async def total(id: str) -> float:
    order = await apps.orders.tools.lookup({"id": id})
    return order["total"]
```

TypeScript:

```typescript
import { apps } from "holon-sh";

export async function total(id: string): Promise<number> {
  const order = await apps.orders.tools.lookup({ id });
  return order.total;
}
```

The argument and the result are typed from the provider's schemas. A misspelled field is a type error; a tool error raises `ToolCallError`.

### 4. Run it

The client needs the provider's URL. Set the variable named after the dependency:

```sh
ORDERS_MCP_URL=http://127.0.0.1:3001/mcp uv run python server.py
```

Authentication, custom HTTP clients and test fixtures are explicit bindings; see the runtime docs.

## Catch breaking changes in CI

```sh
holon types check . ../orders
```

This regenerates the clients into a temporary copy of each app and runs the language's type checker (mypy or `tsc`) there. If `orders` renames `total`, `checkout` fails here, before anything is deployed. `holon check` is the same command.

## Call a server you don't own

A `requires` entry that matches none of the listed directories is looked up in the [MCP Registry](https://registry.modelcontextprotocol.io) by name and version. The CLI takes the contract from the first source the record offers: a published contract document (below), a contract stored beside the server's container image, or a live `tools/list` against the server. If none is available, generation still succeeds with an untyped client for that dependency and a note on stderr.

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

## Where to go next

- [Python runtime](https://github.com/holon-sh/sdk-py): tool results, errors, explicit bindings with auth, serving MCP App views.
- [Node.js runtime](https://github.com/holon-sh/sdk-ts): the same for TypeScript, plus typed resources.
- [Browser runtime](https://github.com/holon-sh/sdk-ui): calling your own server from an MCP Apps view, exposing tools to a WebMCP agent.

## License

MIT
