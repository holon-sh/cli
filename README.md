# holon

[![CI](https://github.com/holon-sh/cli/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/holon-sh/cli/actions/workflows/ci.yml)
[![Apache 2.0 License](https://img.shields.io/badge/license-Apache--2.0-green)](LICENSE)
[![Discord](https://img.shields.io/badge/Discord-join-5865F2?logo=discord&logoColor=white)](https://discord.gg/jXExyBdy)

`holon` provides type-safety for MCP servers, e.g. invoking an MCP server as a function call your type checker understands, through the [Python SDK](https://github.com/holon-sh/sdk-py), [TypeScript SDK](https://github.com/holon-sh/sdk-ts) and [Go SDK](https://github.com/holon-sh/sdk-go).

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

`--lang ts` or `--lang go` picks another language. The app is an MCP server with one tool, `greet`, and an [MCP App](https://modelcontextprotocol.io/extensions/apps/overview) view that calls it through a typed client:

```ts
// orders/ui/main.ts
const { result } = await server.tools.greet({ name: name.value });
```

`holon init` installs the dependencies, generates the clients and adds the app to `holon.yaml`. Run it inside an existing project to add an app beside its files.

## Example

Declare dependencies under `sh.holon.requires` in your project's [`server.json`](https://modelcontextprotocol.io/registry/quickstart#step-4-create-server-json), here the public [Hugging Face](https://huggingface.co/docs/hub/en/hf-mcp-server) MCP server.

```json
{
  "name": "com.example/foo",
  "version": "0.1.0",
  "description": "Example Foo",
  "_meta": {
    "io.modelcontextprotocol.registry/publisher-provided": {
      "sh.holon": {
        "requires": {
          "huggingface": { "url": "https://huggingface.co/mcp" }
        }
      }
    }
  }
}
```

Generate the clients and type-check the calls to them:

```sh
holon types check
```

Or name them with `-s`, which replaces `requires`:

```sh
holon types check -s huggingface=https://huggingface.co/mcp
```

A wrong tool name or argument is a type error:

### Python

```python
from holon.apps import apps

await apps.huggingface.tools.hub_repo_search(query="llama")

await apps.huggingface.tools.hub_repo_search(limit="3")
# error: Argument "limit" to "hub_repo_search" of "HuggingfaceTools" has incompatible type "str"; expected "float"  [arg-type]

await apps.huggingface.tools.hub_repo_details()
# error: Missing named argument "repo_ids" for "hub_repo_details" of "HuggingfaceTools"  [call-arg]

await apps.huggingface.tools.hub_search(query="llama")
# error: "HuggingfaceTools" has no attribute "hub_search"; maybe "hub_repo_search"?  [attr-defined]
```

### TypeScript

```ts
import { apps } from "#holon-sh";

await apps.huggingface.tools.hub_repo_search({ query: "llama" });

await apps.huggingface.tools.hub_repo_search({ limit: "3" });
// error TS2322: Type 'string' is not assignable to type 'number'.

await apps.huggingface.tools.hub_repo_details({});
// error TS2741: Property '"repo_ids"' is missing in type '{}' but required in type '{ config?: string | undefined; limit?: number | undefined; offset?: number | undefined; operations?: ("dataset_preview" | "dataset_structure" | "overview")[] | undefined; repo_ids: string[]; repo_type?: "dataset" | ... 2 more ... | undefined; split?: string | undefined; }'.

await apps.huggingface.tools.hub_search({ query: "llama" });
// error TS2339: Property 'hub_search' does not exist on type '{ readonly hf_fs: (input: { operations: { args: string[]; cmd: "attach" | "cat" | "find" | "ls" | "search" | "stat"; }[]; }, options?: ToolCallOptions | undefined) => Promise<{ results: ({ ...; } | { ...; })[]; truncated?: boolean | undefined; truncation_reason?: "output_budget" | undefined; }>; readonly hf_whoami: ...'.
```

## Dependencies

A dependency is an MCP server your project calls. `holon` reads the JSON Schemas of its tools and resources and writes a typed client, `apps.<alias>`, into your project. A dependency is one of:

### Source code

An MCP server's entry file or directory, such as one not published yet:

```python
# bar/server.py
from mcp.server import MCPServer

server = MCPServer("com.example/bar", version="0.1.0", description="Example Bar")


@server.tool()
def greet(name: str) -> str:
    return f"hello {name}"
```

```sh
holon types check -s bar/server.py
```

### Registry

An [MCP Registry](https://modelcontextprotocol.io/registry/about) version URL:

```sh
holon types check -s https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4
```

### Server

A running server's URL:

```sh
holon types check -s https://huggingface.co/mcp
```

If it requires a token, name it and set `<ALIAS>_MCP_TOKEN`, which is sent only to that origin:

```sh
export DOCS_MCP_TOKEN=...
holon types check -s docs=https://mcp.example.com/mcp
```

## Community

Ask questions and share what you're building on the [Holon Discord](https://discord.gg/jXExyBdy).

## Reference

See the [CLI reference](https://holon-labs.dev/docs/#cli-reference).
