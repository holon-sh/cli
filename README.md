# holon

[![CI](https://github.com/holon-sh/cli/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/holon-sh/cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go)](https://go.dev/doc/install)
[![Downloads](https://img.shields.io/badge/releases-download-blue)](https://github.com/holon-sh/cli/releases/latest)
[![MIT License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

`holon` provides type-safety for MCP servers, e.g. invoking an MCP server as a function call your type checker understands.

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

await apps.context7.call_tool("resolve-library-id", libraryName="Next.js", query="app router")

await apps.context7.call_tool("resolve-library-id", libraryName=1, query="app router")
# error: No overload variant of "call_tool" of "Context7App" matches argument types "str", "int", "str"  [call-overload]

await apps.context7.call_tool("resolve-library", libraryName="Next.js", query="app router")
# error: No overload variant of "call_tool" of "Context7App" matches argument types "str", "str", "str"  [call-overload]
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
holon types check foo -s bar/server.py
```

<details open>
<summary><h4>Python</h4></summary>

```python
from holon.apps import apps

greeting = await apps.bar.tools.greet(name="world")
greeting["result"]

await apps.bar.tools.greet(name=1)
# error: Argument "name" to "greet" of "BarTools" has incompatible type "int"; expected "str"  [arg-type]

await apps.bar.tools.hello(name="world")
# error: "BarTools" has no attribute "hello"  [attr-defined]

greeting["text"]
# error: TypedDict "BarGreetOutput" has no key "text"  [typeddict-item]
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

## Dependencies

A dependency is an MCP server your project calls. For each one `holon` obtains a contract—the JSON Schemas of the server's tools and resources—and writes a typed client `apps.<alias>` into the SDK package your project declares. `holon types check` then runs the language's type checker over your code against those clients.

A dependency is written in one of three ways.

<details open>
<summary><h3>Source code</h3></summary>

An implementation of an MCP server, or its parent directory.

```sh
holon types check -s bar/server.py
```

</details>

<details open>
<summary><h3>Registry</h3></summary>

A `sh.holon.requires` entry in [`server.json`](https://modelcontextprotocol.io/registry/quickstart#step-4-create-server-json), or `-s` with a registry version URL. The record is fetched for its contract.

```sh
holon types check -s https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4
```

</details>

<details open>
<summary><h3>Server</h3></summary>

An endpoint URL. The running server is asked for `tools/list` and `resources/list`.

```sh
holon types check -s https://mcp.context7.com/mcp
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

## Contract documents

Images publish `application/vnd.holon.mcp-contract.v1+json` documents with an
`identity`, an optional `schema` containing tools and resources, and `requires`
mapping dependency aliases to SHA-256 blob digests. The image index annotation
`sh.holon.contract.digest` points to the own document. Each dependency document
is stored in the consumer's repository, including snapshots observed from live
MCP endpoints. Readers verify the fetched bytes before comparing schemas and
provider identities.

This replaces inline dependency contracts and `sh.holon.types` document URLs.
Rebuild and republish alpha images, and regenerate client slots. Remove legacy
managed `holon/` runtime copies first; generation only replaces marked slots.
Python apps install `holon-sh` and import generated clients with
`from holon.apps import apps`.
