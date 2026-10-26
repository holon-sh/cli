# holon

[![CI](https://github.com/holon-sh/cli/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/holon-sh/cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go)](https://go.dev/doc/install)
[![Downloads](https://img.shields.io/badge/releases-download-blue)](https://github.com/holon-sh/cli/releases/latest)
[![MIT License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

`holon` generates typed clients for MCP servers. This allows invoking an MCP server as a function call your type checker understands.

This is currently supported via the [Python SDK](https://github.com/holon-sh/sdk-py) and [TypeScript SDK](https://github.com/holon-sh/sdk-ts).

```mermaid
flowchart LR
  R["server.json<br/>sh.holon.requires"] --> H["holon types check"]
  H -->|fetches| C["each server's contract<br/>(tool and resource schemas)"]
  C --> TS["node_modules/holon-sh/generated/"] --> TSC["tsc"]
  C --> PY["holon/apps/__init__.py"] --> MYPY["mypy"]
```

## Install

```sh
curl -fsSL https://holon-labs.dev/install.sh | sh
```

Or alternatively with [Go](https://go.dev/doc/install) 1.27+, build and install from source:

```sh
go install github.com/holon-sh/cli/cmd/holon@latest
```

## Walkthrough

**1. Declare dependencies** under `sh.holon.requires` in your project's [`server.json`](https://modelcontextprotocol.io/registry/quickstart#step-4-create-server-json) ([publisher-provided metadata](https://modelcontextprotocol.io/registry/faq#can-i-add-custom-metadata-when-publishing)). The key names the client (`apps.orders`) and its endpoint variable at run time (`ORDERS_MCP_URL`); the value is an exact registry version.

```json
{
  "name": "io.github.acme/checkout",
  "version": "0.1.0",
  "description": "Checkout service",
  "_meta": {
    "io.modelcontextprotocol.registry/publisher-provided": {
      "sh.holon": {
        "requires": {
          "orders": { "name": "io.github.acme/orders", "version": "1.0.0" }
        }
      }
    }
  }
}
```

**2. Generate and check.** The project must depend on `holon-sh` (`package.json`, `pyproject.toml` or `requirements.txt`).

```sh
holon types check
```

**3. Call the server.** A wrong argument or result field is a type error.

```ts
import { apps } from "holon-sh";
const order = await apps.orders.tools.lookup({ id });
```

```python
from holon.apps import apps
order = await apps.orders.tools.lookup({"id": id})
```

## Commands

| Command | What it does |
|---|---|
| `holon types generate [DIR]` | Resolve the dependencies, write the typed clients. |
| `holon types check [DIR]` (or `holon check`) | Generate, then type-check `DIR`'s calls to them. |
| `holon types check --no-generate` | Type-check the clients already generated: no network, no install, no writes. |
| `... -s [ALIAS=]PATH\|URL` (repeatable) | Use these dependencies instead of `requires`. Any `-s` replaces the whole list. |
| `holon types generate --contract SERVER` | Print one server's contract and write nothing. |

`DIR` is the project, default `.`. Where a dependency's contract comes from:

```mermaid
flowchart TD
  D["dependency"] --> P["server directory or entry file<br/>-s ../orders"]
  D --> G["registry server<br/>requires entry, or -s https://registry.modelcontextprotocol.io/v0.1/servers/…/versions/…"]
  D --> L["running MCP server<br/>-s docs=https://mcp.context7.com/mcp"]
  P --> PX["extracted from source"]
  G --> G1{"sh.holon.types<br/>document?"} -->|yes| GX["fetched"]
  G1 -->|no| G2{"OCI package with<br/>a contract artifact?<br/>(holon publish)"} -->|yes| GI["read from the image"]
  G2 -->|no| G3{"streamable-http<br/>remote?"} -->|yes| GL["tools/list, resources/list"]
  G3 -->|no| GE["error"]
  L --> LX["tools/list, resources/list"]
```

## Reference

<details>
<summary><code>holon types generate</code></summary>

```text
Generate typed clients for MCP servers.

Leverages sh.holon.requires in the publisher-provided metadata field (https://modelcontextprotocol.io/registry/faq#can-i-add-custom-metadata-when-publishing) from server.json (https://modelcontextprotocol.io/registry/quickstart#step-4-create-server-json) to resolve dependencies, or the -s list for explicit dependencies.

Usage:
  holon types generate [DIR] [flags]

Examples:
  # Generate for the current project, from server.json requires
  holon types generate

  # Another project
  holon types generate ../checkout

  # Explicit dependencies: a server directory, a server entry, an MCP registry server, a remote MCP server named docs
  holon types generate -s ../orders
  holon types generate -s ../orders/server.py
  holon types generate -s https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4
  holon types generate -s docs=https://mcp.context7.com/mcp

  # Print one server's contract
  holon types generate --contract ../orders

Flags:
      --contract string      Print one server's MCP contract instead of generating
  -h, --help                 help for generate
  -s, --server stringArray   A dependency, [ALIAS=]PATH or URL, paths relative to the working directory; repeatable. Without it, server.json requires is used
  -v, --verbose              Show generation details
```

</details>

<details>
<summary><code>holon types check</code></summary>

```text
Generate typed clients for MCP servers, then type-check the project's calls to them.

Leverages sh.holon.requires in the publisher-provided metadata field (https://modelcontextprotocol.io/registry/faq#can-i-add-custom-metadata-when-publishing) from server.json (https://modelcontextprotocol.io/registry/quickstart#step-4-create-server-json) to resolve dependencies, or the -s list for explicit dependencies.

Usage:
  holon types check [DIR] [flags]

Examples:
  # Generate from server.json requires, then type-check
  holon types check

  # Type-check the existing clients only (no network, no writes)
  holon types check --no-generate

  # Explicit dependencies, as for generate
  holon types check -s orders=../orders

Flags:
  -h, --help                 help for check
      --no-generate          Type-check the existing clients only: no network, no install, no writes
  -s, --server stringArray   A dependency, [ALIAS=]PATH or URL, paths relative to the working directory; repeatable. Without it, server.json requires is used
```

</details>
