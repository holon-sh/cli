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

Or alternatively with [Go](https://go.dev/doc/install) 1.27+, build and install from source:

```sh
go install github.com/holon-sh/cli/cmd/holon@latest
```

## Usage

<details open>
<summary><code>holon types generate</code></summary>

```text
Generate typed clients for the MCP servers a project calls, into the runtime it declares (holon-sh or holon-ui in package.json, or holon-sh in pyproject.toml).

Run it in the project that calls the servers. Each SERVER is a server entry (server.py, server.ts or
server.js, at the root or under src/) or its directory, an MCP registry version URL, or an MCP endpoint
URL; ALIAS= names its client. A server inside the project is the project's own, for its holon-ui view.

Usage:
  holon types generate SERVER... [flags]

Flags:
      --contract   Print one server's MCP contract instead of generating
  -h, --help       help for generate
  -v, --verbose    Show generation details
```

</details>

<details open>
<summary><code>holon types check</code></summary>

```text
Type-check the project's calls to these servers, in a temporary copy.

Run it in the project that calls the servers. Each SERVER is a server entry (server.py, server.ts or
server.js, at the root or under src/) or its directory, an MCP registry version URL, or an MCP endpoint
URL; ALIAS= names its client. A server inside the project is the project's own, for its holon-ui view.

Usage:
  holon types check SERVER... [flags]

Flags:
  -h, --help   help for check
```

</details>
