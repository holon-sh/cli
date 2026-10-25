# holon

`holon` generates typed clients for MCP servers. This allows invoking an MCP server as a function call your type checker understands.

This is currently supported via the [Python SDK](https://github.com/holon-sh/sdk-py), [TypeScript SDK](https://github.com/holon-sh/sdk-ts), and [UI SDK](https://github.com/holon-sh/sdk-ui).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/holon-sh/cli/main/install.sh | sh
```

## Examples

[`examples/`](examples) contains a Python `orders` MCP and a TypeScript `checkout` MCP that calls it and renders a UI.

```sh
git clone https://github.com/holon-sh/cli && cd cli/examples
(cd orders && uv sync)
(cd checkout && npm install)
```

### 1. Generate the client

Run the following to generate a type-safe client that `checkout` can use to invoke `orders`:

```sh
cd checkout
holon types generate . ../orders
```

### 2. Call the tool

In [`examples/checkout/server.ts`](examples/checkout/server.ts), this allows us to write the following:

```typescript
import { apps } from "holon-sh";

// ...

const order = await apps.orders.tools.lookup({ id }); // { id: string; total: number }
```

as opposed to:

```typescript
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";

// ...

const client = new Client({ name: "checkout", version: "1.0.0" });
await client.connect(new StreamableHTTPClientTransport(new URL(process.env.ORDERS_MCP_URL!)));
const result = await client.callTool({ name: "lookup", arguments: { id } });
const order = result.structuredContent as { id: string; total: number }; // unchecked
```

### 3. Add a view

In [`examples/checkout/ui/main.ts`](examples/checkout/ui/main.ts), the view calls `checkout`'s own tool through the MCP Apps host:

```typescript
import { createApps } from "holon-ui";

// ...

const apps = createApps({ checkout: { host: view } });
const { total } = await apps.checkout.tools.total({ id }); // { total: number }
```

as opposed to:

```typescript
const result = await view.callServerTool({ name: "total", arguments: { id } });
const { total } = result.structuredContent as { total: number }; // unchecked
```

### 4. Check in CI

```sh
holon types check . ../orders
```

Fails when a provider's schema no longer matches the caller's code.

## Call a server you don't own

Declare it in `server.json` by its [MCP Registry](https://registry.modelcontextprotocol.io) name and version:

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

The contract comes from the registry record: a published contract document, one stored beside the container image, or a live `tools/list`. With none, the client is untyped.

## Publish your server's contract

```sh
holon types generate --contract > contract.json
```

Host the file and reference it from `server.json`:

```json
"sh.holon": { "types": "https://example.com/orders/1.0.0/contract.json" }
```

## Commands

| Command | What it does |
|---|---|
| `holon types generate [DIR...]` | Generate typed clients for the listed apps (default: the current directory). |
| `holon types generate --contract [DIR]` | Print the app's contract document; write nothing. |
| `holon types check [DIR...]` | Regenerate into temporary copies and type-check. `holon check` is an alias. |

`generate` also takes `--no-install` (print the command that would add a missing runtime) and `--quiet`.

## License

MIT
