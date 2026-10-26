# holon

[![CI](https://github.com/holon-sh/cli/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/holon-sh/cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go)](https://go.dev/doc/install)
[![Downloads](https://img.shields.io/badge/releases-download-blue)](https://github.com/holon-sh/cli/releases/latest)
[![MIT License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

`holon` generates typed clients for MCP servers. This allows invoking an MCP server as a function call your type checker understands.

This is currently supported via the [Python SDK](https://github.com/holon-sh/sdk-py), [TypeScript SDK](https://github.com/holon-sh/sdk-ts), and [UI SDK](https://github.com/holon-sh/sdk-ui).

## Install

```sh
curl -fsSL https://holon-labs.dev/install.sh | sh
```

Or alternatively

With [Go 1.27 or newer](https://go.dev/doc/install), build and install from source:

```sh
go install github.com/holon-sh/cli/cmd/holon@latest
```

## Usage

<details open>
<summary><code>holon types generate</code></summary>

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

<details open>
<summary><code>holon types check</code></summary>

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

The [`examples`](examples) directory contains MCP servers implemented in different languages that make type-safe invocations.

In [`examples/checkout/server.ts`](examples/checkout/server.ts), it invokes a tool in the same repository as

```typescript
import { apps } from "holon-sh";

const order = await apps.orders.tools.lookup({ id }); // { id: string; total: number }
```

<details>
<summary>Without <code>holon</code></summary>

```typescript
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";

const client = new Client({ name: "checkout", version: "1.0.0" });
await client.connect(
  new StreamableHTTPClientTransport(new URL("http://localhost:3001/mcp")),
);
const result = await client.callTool({
  name: "lookup",
  arguments: { id },
});
const order = result.structuredContent as { id: string; total: number }; // unchecked
```

</details>

In [`examples/checkout/server.ts`](examples/checkout/server.ts), it invokes a tool from a remote server as

```typescript
import { apps } from "holon-sh";

const result = await apps.context7.callTool({
  name: "resolve-library-id",                 // tool names are checked
  arguments: { libraryName: library, query }, // arguments are typed
});
```

<details>
<summary>Without <code>holon</code></summary>

```typescript
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";

const client = new Client({ name: "checkout", version: "1.0.0" });
await client.connect(
  new StreamableHTTPClientTransport(new URL("https://mcp.context7.com/mcp")),
);
const result = await client.callTool({
  name: "resolve-library-id",                 // unchecked
  arguments: { libraryName: library, query }, // unchecked
});
```

</details>

In [`examples/checkout/ui/main.ts`](examples/checkout/ui/main.ts), an [MCP Apps](https://modelcontextprotocol.io/extensions/apps/overview) view invokes a tool as

```typescript
import { App } from "holon-ui";

const view = new App({ name: "checkout-view", version: "1.0.0" });
await view.connect();
const { total } = await view.tools.total({ id }); // { total: number }
```

<details>
<summary>Without <code>holon</code></summary>

```typescript
import { App } from "@modelcontextprotocol/ext-apps";

const view = new App({ name: "checkout-view", version: "1.0.0" });
await view.connect();

const result = await view.callServerTool({ name: "total", arguments: { id } });
const { total } = result.structuredContent as { total: number }; // unchecked
```

</details>
