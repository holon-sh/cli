# holon-types

The standalone Holon CLI generates and checks typed MCP clients.

```sh
cd my-project
holon types generate ../orders https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4
holon types check
holon types generate --contract ../orders
```

Each SERVER is a server entry file or directory, an MCP registry server version
URL, or an MCP endpoint URL, optionally prefixed with `ALIAS=`; the client is
`apps.<alias>`, named after the server by default. Clients go into the runtime
the project declares: `holon-sh` in `package.json`, or `holon-sh`
in `pyproject.toml`. `holon types check` type-checks the project.

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

