# holon-types

The standalone Holon CLI generates and checks typed MCP clients.

```sh
uv tool install holon-types
holon types generate
holon check --no-published
```

This package bundles the native `holon` executable. Supported platforms are
macOS 15 or newer and Linux, on arm64 and x86-64. Node 22+ or Python 3.11+ is
required to analyze applications written in the corresponding language.

Packages are prepared locally; registry publication has not been enabled. Dev
builds are published only when the repository owner dispatches
`.github/workflows/publish-dev.yml` by hand.

## Dev releases

Dev builds are versioned `0.0.1.dev<run number>` and published to PyPI as
platform wheels for macOS and Linux on arm64 and x86-64. The version is set
inside the workflow run; the repository stays at `0.0.1`.

```sh
uv tool install --prerelease allow holon-types
uv tool install holon-types==0.0.1.dev12   # exact version from the job summary
pip install --pre holon-types
```

Maintainers: add a pending (trusted) publisher on pypi.org for the project
`holon-types` (owner `holon-sh`, repository `cli`, workflow `publish-dev.yml`,
no environment), or store an API token as the `PYPI_TOKEN` repository secret,
then run `gh workflow run publish-dev.yml`.

