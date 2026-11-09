# holon

[![CI](https://github.com/holon-sh/cli/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/holon-sh/cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go)](https://go.dev/doc/install)
[![Downloads](https://img.shields.io/badge/releases-download-blue)](https://github.com/holon-sh/cli/releases/latest)
[![Apache 2.0 License](https://img.shields.io/badge/license-Apache--2.0-green)](LICENSE)
[![Discord](https://img.shields.io/badge/Discord-join-5865F2?logo=discord&logoColor=white)](https://discord.gg/jXExyBdy)

[`holon`](https://github.com/holon-sh/cli) is a framework for [type-safe](https://holon-labs.dev/docs/#types) [MCP servers](https://modelcontextprotocol.io/docs/learn/server-concepts).

This is currently supported via the [Python SDK](https://github.com/holon-sh/sdk-py), [TypeScript SDK](https://github.com/holon-sh/sdk-ts) and [Go SDK](https://github.com/holon-sh/sdk-go).

## Install

```sh
curl -fsSL https://holon-labs.dev/install.sh | sh
```

Or with [Go](https://go.dev/doc/install) 1.27+:

```sh
go install github.com/holon-sh/cli/cmd/holon@latest
```

## Example

### Create an app

```sh
holon init orders --lang py
```

### Dependencies

Declare dependencies under `sh.holon.requires` in your project's [`server.json`](https://modelcontextprotocol.io/registry/quickstart#step-4-create-server-json) ([publisher-provided metadata](https://modelcontextprotocol.io/registry/faq#can-i-add-custom-metadata-when-publishing)). This example uses the public [Hugging Face](https://huggingface.co/docs/hub/en/hf-mcp-server) MCP server.

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

### Types

Generate the typed clients and type-check the calls to them.

```sh
holon types check
```

Or name the servers yourself. Any `-s` replaces the whole `requires` list, so `server.json` is not needed.

```sh
holon types check -s huggingface=https://huggingface.co/mcp
```

Call the server. A wrong tool name or argument is a type error.

<details open>
<summary><h4>Python</h4></summary>

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

</details>

<details open>
<summary><h4>TypeScript</h4></summary>

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

## Community

Ask questions and share what you're building on the [Holon Discord](https://discord.gg/jXExyBdy).

## Reference

See the [CLI reference](https://holon-labs.dev/docs/#cli-reference).
