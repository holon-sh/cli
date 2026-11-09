# holon

[![CI](https://github.com/holon-sh/cli/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/holon-sh/cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go)](https://go.dev/doc/install)
[![Downloads](https://img.shields.io/badge/releases-download-blue)](https://github.com/holon-sh/cli/releases/latest)
[![Apache 2.0 License](https://img.shields.io/badge/license-Apache--2.0-green)](LICENSE)

`holon` provides type-safety for MCP servers, e.g. invoking an MCP server as a function call your type checker understands.

This is currently supported via the [Python SDK](https://github.com/holon-sh/sdk-py), [TypeScript SDK](https://github.com/holon-sh/sdk-ts) and [Go SDK](https://github.com/holon-sh/sdk-go).

## Install

```sh
curl -fsSL https://holon-labs.dev/install.sh | sh
```

Or with [Go](https://go.dev/doc/install) 1.27+:

```sh
go install github.com/holon-sh/cli/cmd/holon@latest
```

## Create an app

```sh
holon init orders --lang py
```

`--lang ts` writes the server in TypeScript instead, and `--lang go` in Go. Each app has an MCP server with one tool, `greet`, and a view the server returns as an [MCP App](https://modelcontextprotocol.io/extensions/apps/overview) and Holon serves as a page. The view calls the tool through a typed client:

```ts
// orders/ui/main.ts
const { result } = await server.tools.greet({ name: name.value });
```

`holon init` pins the Holon SDKs that read the clients it writes, installs the dependencies (`uv` for Python, `go get` for Go, the project's package manager for Node.js), generates the clients, and adds the app to `holon.yaml`. `holon types check` then type-checks the server and the view. A Go server's contract isn't read from its source, so its view calls `greet` untyped, and `go build` type-checks the server. Run the app with [`holon dev`](https://holon-labs.dev/docs/).

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

await apps.context7.tools.resolve_library_id(libraryName="Next.js", query="app router")

await apps.context7.tools.resolve_library_id(libraryName=1, query="app router")
# error: Argument "libraryName" to "resolve_library_id" of "Context7Tools" has incompatible type "int"; expected "str"  [arg-type]

await apps.context7.tools.resolve_library(libraryName="Next.js", query="app router")
# error: "Context7Tools" has no attribute "resolve_library"; maybe "resolve_library_id"?  [attr-defined]
```

</details>

<details open>
<summary><h4>TypeScript</h4></summary>

```ts
import { apps } from "#holon-sh";

const libraries = await apps.context7.tools["resolve-library-id"]({ libraryName: "Next.js", query: "app router" });
libraries.content; // Context7 declares no output schema, so the complete result is returned

await apps.context7.tools["resolve-library-id"]({ libraryName: 1, query: "app router" });
// error TS2322: Type 'number' is not assignable to type 'string'.

await apps.context7.tools["resolve-library-id"]({ libraryName: "Next.js" });
// error TS2741: Property '"query"' is missing in type '{ libraryName: string; }' but required in type '{ libraryName: string; query: string; }'.

await apps.context7.tools["resolve-library"]({ libraryName: "Next.js", query: "app router" });
// error TS2551: Property 'resolve-library' does not exist on type '{ readonly "query-docs": (input: { language?: string | undefined; libraryId: string; query: string; }, options?: CallOptions | undefined) => Promise<CompleteResult<unknown>>; readonly "resolve-library-id": (input: { ...; }, options?: CallOptions | undefined) => Promise<...>; }'. Did you mean 'resolve-library-id'?
```

</details>

### Development Servers

Let's say you have a server that you're still developing, and it's not in any [MCP Registry](https://modelcontextprotocol.io/registry/about) yet.

```python
# bar/server.py
from mcp.server import MCPServer

server = MCPServer("com.example/bar", version="0.1.0", description="Example Bar")


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
import { apps } from "#holon-sh";

const greeting = await apps.bar.tools.greet({ name: "world" });
greeting.result;

await apps.bar.tools.greet({ name: 1 });
// error TS2322: Type 'number' is not assignable to type 'string'.

await apps.bar.tools.hello({ name: "world" });
// error TS2339: Property 'hello' does not exist on type '{ readonly greet: (input: { name: string; }, options?: CallOptions | undefined) => Promise<{ result: string; }>; }'.

greeting.text;
// error TS2339: Property 'text' does not exist on type '{ result: string; }'.
```

</details>

## Dependencies

A dependency is an MCP server your project calls. For each one `holon` obtains a contract—the JSON Schemas of the server's tools and resources—and writes a typed client `apps.<alias>` into your project, for the SDK it declares: `generated/holon-sh/` for `holon-sh`, `generated/holon-ui/` for `holon-ui`, `holon/apps/` for Python and `holon/` for Go. TypeScript imports them as `#holon-sh` or `#holon-ui`, names `holon` adds to the `imports` of your `package.json`. A package that publishes code calling its clients lists `generated/holon-sh` in its `files`, so the projects that install it need not generate. `holon types check` then runs the language's type checker over your code against those clients. A TypeScript project needs a `tsconfig.json`, found as `tsc` finds it.

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

If the server requires a token, name it and set `<ALIAS>_MCP_TOKEN`. The token is sent only to that URL's origin, as a bearer token, and is not written anywhere.

```sh
export DOCS_MCP_TOKEN=...
holon types check -s docs=https://mcp.example.com/mcp
```

</details>

## Reference

<details open>
<summary><code>holon init</code></summary>

```text
Create an app in DIR, or the working directory: an MCP server in Go, Python or TypeScript, and a
view it serves as an MCP App and as a page.

DIR must be empty or absent; its name is the app's name. holon init pins the Holon SDKs that read the
clients this holon writes, installs the dependencies, generates the clients, and adds the app to the
working directory's holon.yaml when DIR is inside it, or else writes one in DIR.

Usage:
  holon init [DIR] [flags]

Examples:
  # A Go app in ./orders
  holon init orders --lang go

  # A Python app in ./orders
  holon init orders --lang py

  # A TypeScript app in the working directory, with its MCP server name
  holon init --lang ts --name io.github.acme/orders

Flags:
  -h, --help          help for init
      --lang string   The server's language: go, py or ts
      --name string   The MCP server name (default com.example/APP)
```

</details>

<details open>
<summary><code>holon types generate</code></summary>

```text
Generate typed clients for MCP servers.

Leverages sh.holon.requires in the publisher-provided metadata field
(https://modelcontextprotocol.io/registry/faq#can-i-add-custom-metadata-when-publishing) to
resolve dependencies, or the -s list for explicit dependencies.

Usage:
  holon types generate [DIR] [flags]

Examples:
  # Generate for the current project
  holon types generate

  # Another project
  holon types generate ../checkout

  # Name the project's own server to call it from outside the browser too, as apps.orders
  holon types generate -s orders=./server

  # Explicit dependencies: a server directory, a server entry, an MCP registry server, a remote
  #   MCP server named docs
  holon types generate -s ../orders
  holon types generate -s ../orders/server.py
  holon types generate -s https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4
  holon types generate -s docs=https://mcp.context7.com/mcp

  # A remote MCP server that requires a token: DOCS_MCP_TOKEN is sent to it as a bearer token
  DOCS_MCP_TOKEN=... holon types generate -s docs=https://mcp.example.com/mcp

  # Print one server's contract, or commit it beside its server.json
  holon types generate --contract ../orders
  holon types generate --contract ../orders -o ../orders/holon-contract.json

Flags:
      --contract string      Print one server's MCP contract instead of generating
  -h, --help                 help for generate
  -o, --output string        Write the --contract document to FILE, such as holon-contract.json
  -s, --server stringArray   A dependency, [ALIAS=]PATH or URL, paths relative to the working
                             directory
  -v, --verbose              Show generation details
```

</details>

<details open>
<summary><code>holon types check</code></summary>

```text
Generate typed clients for MCP servers, then type-check the project's calls to them.

Leverages sh.holon.requires in the publisher-provided metadata field
(https://modelcontextprotocol.io/registry/faq#can-i-add-custom-metadata-when-publishing) to
resolve dependencies, or the -s list for explicit dependencies.

Usage:
  holon types check [DIR] [flags]

Examples:
  # Generate, then type-check
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
                             directory
```

</details>
