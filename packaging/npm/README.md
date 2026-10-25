# holon-cli

The standalone Holon CLI generates and checks typed MCP clients.

```sh
uv tool install holon-cli
holon types generate
holon check --no-published
```

This package bundles the native `holon` executable. Supported platforms are
macOS 15 or newer and Linux, on arm64 and x86-64. Node 22+ or Python 3.11+ is
required to analyze applications written in the corresponding language.

Packages are prepared locally; registry publication has not been enabled.
