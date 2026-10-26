# holon-types

The holon CLI generates typed clients for MCP servers and type-checks calls to them.
This package bundles the native executable for macOS and Linux on arm64 and x86-64.

In a Python project:

```sh
uv add --dev holon-types
uv run holon types check
```

In a Node project:

```sh
npm install -D holon-types
npx holon types check
```

Without adding a project dependency: `uvx --from holon-types holon types check` or `npx holon-types types check`.

See the [repository README](https://github.com/holon-sh/cli#readme) for usage.
