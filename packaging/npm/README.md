# holon-types

The standalone Holon CLI generates and checks typed MCP clients.

```sh
cd my-mcp-server            # a directory with server.json
holon types generate        # or: holon types generate ./checkout ./orders
holon check
holon types contract
```

Directories listed together resolve each other's `requires` by the
`server.json` name; everything else comes from the MCP registry.

This package bundles the native `holon` executable. Supported platforms are
macOS 15 or newer and Linux, on arm64 and x86-64. Node 22+ or Python 3.11+ is
required to analyze applications written in the corresponding language.

Packages are prepared locally; registry publication has not been enabled. Dev
builds are published only when the repository owner dispatches
`.github/workflows/publish-dev.yml` by hand.

## Dev releases

Dev builds are versioned `0.0.1-dev.<run number>` and published under the npm
`dev` dist-tag (never `latest`), as `holon-types` plus the
`holon-types-<os>-<arch>` platform packages. The version is set inside the
workflow run; the repository stays at `0.0.1`.

```sh
npm install -g holon-types@dev
npm install -g holon-types@0.0.1-dev.12   # exact version from the job summary
```

Maintainers: configure a trusted publisher on npmjs.com for each of the five
packages (owner `holon-sh`, repository `cli`, workflow `publish-dev.yml`, no
environment), or store an automation token as the `NPM_TOKEN` repository
secret for the first publish, then run `gh workflow run publish-dev.yml`.

