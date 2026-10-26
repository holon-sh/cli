# holon

[![CI](https://github.com/holon-sh/cli/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/holon-sh/cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go)](https://go.dev/doc/install)
[![Downloads](https://img.shields.io/badge/releases-download-blue)](https://github.com/holon-sh/cli/releases/latest)
[![MIT License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

`holon` generates typed clients for MCP servers. This allows invoking an MCP server as a function call your type checker understands.

This is currently supported via the [Python SDK](https://github.com/holon-sh/sdk-py) and [TypeScript SDK](https://github.com/holon-sh/sdk-ts).

## Install

```sh
curl -fsSL https://holon-labs.dev/install.sh | sh
```

Or alternatively with [Go](https://go.dev/doc/install) 1.27+, build and install from source:

```sh
go install github.com/holon-sh/cli/cmd/holon@latest
```

## Example

Declare dependencies under `sh.holon.requires` in your project's [`server.json`](https://modelcontextprotocol.io/registry/quickstart#step-4-create-server-json) ([publisher-provided metadata](https://modelcontextprotocol.io/registry/faq#can-i-add-custom-metadata-when-publishing)). This example uses the public [Context7](https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4) server.

```json
{
  "name": "com.example/foo",
  "version": "0.1.0",
  "description": "Example Foo",
  "_meta": {
    "io.modelcontextprotocol.registry/publisher-provided": {
      "sh.holon": {
        "requires": {
          "context7": { "name": "io.github.upstash/context7", "version": "4.4.4" }
        }
      }
    }
  }
}
```

Generate the typed clients and type-check the calls to them.

```sh
holon types check
```

Or name the servers yourself. Any `-s` replaces the whole `requires` list, so `server.json` is not needed.

```sh
holon types check -s https://mcp.context7.com/mcp
```

Call the server. A wrong tool name or argument is a type error.

<details open>
<summary><h4>Python</h4></summary>

```python
from holon.apps import apps

await apps.context7.call_tool("resolve-library-id", {"libraryName": "Next.js", "query": "app router"})

await apps.context7.call_tool("resolve-library-id", {"libraryName": 1, "query": "app router"})
# error: No overload variant of "call_tool" of "_App0" matches argument types "str", "dict[str, object]"  [call-overload]

await apps.context7.call_tool("resolve-library", {"libraryName": "Next.js", "query": "app router"})
# error: No overload variant of "call_tool" of "_App0" matches argument types "str", "dict[str, str]"  [call-overload]
```

</details>

<details open>
<summary><h4>TypeScript</h4></summary>

```ts
import { apps } from "holon-sh";

await apps.context7.tools["resolve-library-id"]({ libraryName: "Next.js", query: "app router" });

await apps.context7.tools["resolve-library-id"]({ libraryName: 1, query: "app router" });
// error TS2322: Type 'number' is not assignable to type 'string'.

await apps.context7.tools["resolve-library-id"]({ libraryName: "Next.js" });
// error TS2345: Property '"query"' is missing in type '{ libraryName: string; }' but required in type '{ libraryName: string; query: string; }'.

await apps.context7.tools["resolve-library"]({ libraryName: "Next.js", query: "app router" });
// error TS2551: Property 'resolve-library' does not exist on type '{ ... }'. Did you mean 'resolve-library-id'?
```

</details>

### Development Servers

Let's say you have a server that you're still developing, and it's not in any [MCP Registry](https://modelcontextprotocol.io/registry/about) yet.

```python
# bar/server.py
from mcp.server import MCPServer

server = MCPServer("bar")


@server.tool()
def greet(name: str) -> str:
    return f"hello {name}"
```

Pass its entry file with `-s`.

```sh
holon types check foo -s https://mcp.context7.com/mcp -s bar/server.py
```

<details open>
<summary><h4>Python</h4></summary>

```python
from holon.apps import apps

greeting = await apps.bar.tools.greet({"name": "world"})
greeting["result"]

await apps.bar.tools.greet({"name": 1})
# error: Incompatible types (expression has type "int", TypedDict item "name" has type "str")  [typeddict-item]

await apps.bar.tools.hello({"name": "world"})
# error: "_Tools0" has no attribute "hello"  [attr-defined]

greeting["text"]
# error: TypedDict "_Shape1" has no key "text"  [typeddict-item]
```

</details>

<details open>
<summary><h4>TypeScript</h4></summary>

```ts
import { apps } from "holon-sh";

const greeting = await apps.bar.tools.greet({ name: "world" });
greeting.result;

await apps.bar.tools.greet({ name: 1 });
// error TS2322: Type 'number' is not assignable to type 'string'.

await apps.bar.tools.hello({ name: "world" });
// error TS2339: Property 'hello' does not exist on type '{ readonly greet: (input: { name: string; }, options?: CallOptions) => Promise<{ result: string; }>; }'.

const text: string = greeting.text;
// error TS2322: Type 'unknown' is not assignable to type 'string'.
```

</details>

## Reference

<details open>
<summary><code>holon types generate</code></summary>

```text
Generate typed clients for MCP servers.

Leverages sh.holon.requires in the publisher-provided metadata field
(https://modelcontextprotocol.io/registry/faq#can-i-add-custom-metadata-when-publishing) from
server.json (https://modelcontextprotocol.io/registry/quickstart#step-4-create-server-json) to
resolve dependencies, or the -s list for explicit dependencies.

Usage:
  holon types generate [DIR] [flags]

Examples:
  # Generate for the current project, from server.json requires
  holon types generate

  # Another project
  holon types generate ../checkout

  # Explicit dependencies: a server directory, a server entry, an MCP registry server, a remote
  #   MCP server named docs
  holon types generate -s ../orders
  holon types generate -s ../orders/server.py
  holon types generate -s https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4
  holon types generate -s docs=https://mcp.context7.com/mcp

  # Print one server's contract
  holon types generate --contract ../orders

Flags:
      --contract string      Print one server's MCP contract instead of generating
  -h, --help                 help for generate
  -s, --server stringArray   A dependency, [ALIAS=]PATH or URL, paths relative to the working
                             directory; repeatable. Without it, server.json requires is used
  -v, --verbose              Show generation details
```

</details>

<details open>
<summary><code>holon types check</code></summary>

```text
Generate typed clients for MCP servers, then type-check the project's calls to them.

Leverages sh.holon.requires in the publisher-provided metadata field
(https://modelcontextprotocol.io/registry/faq#can-i-add-custom-metadata-when-publishing) from
server.json (https://modelcontextprotocol.io/registry/quickstart#step-4-create-server-json) to
resolve dependencies, or the -s list for explicit dependencies.

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
      --no-generate          Type-check the existing clients only: no network, no install, no
                             writes
  -s, --server stringArray   A dependency, [ALIAS=]PATH or URL, paths relative to the working
                             directory; repeatable. Without it, server.json requires is used
```

</details>

### Dependencies

A dependency is an MCP server your project calls. For each one `holon` obtains a **contract**, the JSON Schemas of the server's tools and resources, and writes a typed client `apps.<alias>` into the SDK package your project declares. `check` then runs the language's type checker over your code against those clients.

```mermaid
flowchart LR
  D["dependencies<br/>server.json requires, or -s"] --> C["contracts"] --> G["generated clients<br/>holon-sh/generated (TypeScript)<br/>holon.apps (Python)"] --> T["tsc / mypy"]
```

A dependency is written in one of three ways, and the contract comes from a different place for each.

**A server's source code.** A directory or entry file. The contract is extracted from the code; the server is never started.

```sh
holon types check -s bar/server.py
```

**A registry record.** A `requires` entry in `server.json`, or `-s` with a registry version URL. The record is fetched and its contract is the first of these that the record has:

1. the `sh.holon.types` document it links to;
2. the contract artifact attached to its OCI package, which `holon publish` adds;
3. its `streamable-http` remote, asked live.

```sh
holon types check -s https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4
```

**A running server.** An endpoint URL. The server is asked for `tools/list` and `resources/list`.

```sh
holon types check -s https://mcp.context7.com/mcp
```

The alias names the client: the `requires` key, an `ALIAS=` prefix on `-s`, or otherwise the last segment of the server's name, so Context7 becomes `apps.context7`. Any `-s` replaces the whole `requires` list rather than adding to it.
