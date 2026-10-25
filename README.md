# holon

[![CI](https://github.com/holon-sh/cli/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/holon-sh/cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go)](https://go.dev/doc/install)
[![Downloads](https://img.shields.io/badge/releases-download-blue)](https://github.com/holon-sh/cli/releases/latest)
[![MIT License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

`holon` generates typed clients for MCP servers. This allows invoking an MCP server as a function call your type checker understands.

This is currently supported via the [Python SDK](https://github.com/holon-sh/sdk-py), [TypeScript SDK](https://github.com/holon-sh/sdk-ts), and [UI SDK](https://github.com/holon-sh/sdk-ui).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/holon-sh/cli/main/install.sh | sh
```

## Commands

### Generate

```sh
holon types generate [DIR...]
```

<details open>
<summary><code>holon types generate --help</code></summary>

```text
Generate types in installed SDK slots

Usage:
  holon types generate [DIR...] [flags]

Flags:
      --contract     Print the app's language-neutral MCP contract document instead of generating; writes nothing
  -h, --help         help for generate
      --no-install   Print missing runtime installation commands without running them
      --quiet        Suppress generation progress
```

</details>

### Check

```sh
holon types check [DIR...]
```

<details open>
<summary><code>holon types check --help</code></summary>

```text
Type-check apps against their typed contracts in temporary source copies.

holon check and holon types check are the same command.

Usage:
  holon types check [DIR...] [flags]

Flags:
  -h, --help   help for check
```

</details>

## Examples

[`examples/`](examples) contains a Python `orders` MCP and a TypeScript `checkout` MCP that calls it, calls a public MCP server, and renders a UI.

```sh
git clone https://github.com/holon-sh/cli && cd cli/examples
(cd orders && uv sync)
(cd checkout && npm install)
```

### Generate the client

Run the following to generate a type-safe client that `checkout` can use to invoke `orders`:

```sh
cd checkout
holon types generate . ../orders
```

### Call the tool

In [`examples/checkout/server.ts`](examples/checkout/server.ts), this allows us to write the following:

```typescript
import { apps } from "holon-sh";

// ...

const order = await apps.orders.tools.lookup({ id }); // { id: string; total: number }, validated
```

as opposed to:

```typescript
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";

// ...

const client = new Client({ name: "checkout", version: "1.0.0" });
await client.connect(new StreamableHTTPClientTransport(new URL("http://localhost:3001/mcp")));
const result = await client.callTool({ name: "lookup", arguments: { id } });
const order = result.structuredContent as { id: string; total: number }; // unchecked
```

### Call a server you don't own

[`examples/checkout/server.json`](examples/checkout/server.json) declares [Context7](https://context7.com), a public documentation server that needs no API key, by its [MCP Registry](https://registry.modelcontextprotocol.io) name and version:

```json
"sh.holon": {
  "requires": {
    "context7": { "name": "io.github.upstash/context7", "version": "4.4.4" }
  }
}
```

The same `holon types generate` resolves it through the registry, so `checkout`'s `docs` tool can write:

```typescript
const result = await apps.context7.callTool({
  name: "resolve-library-id",                 // tool names are checked
  arguments: { libraryName: library, query }, // arguments are typed and validated
});
```

```sh
CONTEXT7_MCP_URL=https://mcp.context7.com/mcp ORDERS_MCP_URL=http://localhost:3001/mcp npm run serve
```

### Add a view

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

### Check in CI

```sh
holon types check . ../orders
```

Fails when a provider's schema no longer matches the caller's code.

## Publish your server's contract

```sh
holon types generate --contract > contract.json
```

Host the file and reference it from `server.json`:

```json
"sh.holon": { "types": "https://example.com/orders/1.0.0/contract.json" }
```
