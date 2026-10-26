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
Generate typed clients for MCP servers.

Run it in the project that calls the servers.

Usage:
  holon types generate SERVER... [flags]

Examples:
  # A server directory, or its server.py / server.ts
  holon types generate ../orders

  # An MCP registry server version
  holon types generate https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4

  # A live MCP endpoint, with the client named docs
  holon types generate docs=https://mcp.context7.com/mcp

  # This project's own server, for its holon-ui view
  holon types generate ../orders ./server.ts

Flags:
      --contract   Print one server's MCP contract instead of generating
  -h, --help       help for generate
  -v, --verbose    Show generation details
```

</details>

<details open>
<summary><code>holon types check</code></summary>

```text
Type-check a project's calls to MCP servers.

Run it in the project that calls the servers.

Usage:
  holon types check SERVER... [flags]

Examples:
  # A server directory, or its server.py / server.ts
  holon types check ../orders

  # An MCP registry server version
  holon types check https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4

  # A live MCP endpoint, with the client named docs
  holon types check docs=https://mcp.context7.com/mcp

  # This project's own server, for its holon-ui view
  holon types check ../orders ./server.ts

Flags:
  -h, --help   help for check
```

</details>
