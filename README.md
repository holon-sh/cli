# Holon CLI

A standalone `holon` command for typed MCP contracts. This private repository
has been created on GitHub alongside the public `holon-sh/sdk-ts` and
`holon-sh/sdk-py` repositories. It contains the importable Go package `github.com/holon-sh/cli/pkg/typegen` and
package-manager launchers. It has no container build or cluster bootstrap steps.

```sh
holon types generate              # Fill each app's generated types slot
holon types generate checkout     # Generate one app
holon types contract orders       # Print a local app's contract
holon types contract --url https://example.com/mcp --name example/orders --version 1.0.0
holon check --no-published        # Check locally without querying published metadata
```

Use `-p path/to/holon.yaml` to choose a project. TypeScript apps install
`@holon/apps`; Python apps install `holon-sh` and import
`from holon.apps import apps, Inputs, Outputs`. Node 22+ and Python 3.11+ are
required for extracting the respective languages.

Generation writes TypeScript files into the installed package's `generated/`
directory and Python files into `<app>/holon/apps/__init__.py`. Existing foreign
files are refused. Two apps cannot share the same installed TypeScript slot.
`holon check` uses temporary copies and leaves the app's files unchanged.

## Build and verify

The CLI is pure Go: the contract compiler (`pkg/mcptype`) is a Go port of mcptype
0.0.6 whose outputs are checked byte for byte against that compiler's recorded
answers in `pkg/mcptype/testdata`. Building needs only Go; the tests also need
Node and Python for the extraction tooling.

Vendor the public SDK tooling archives, then test and build:

```sh
scripts/update-tooling ../sdk-ts/dist/cli-tooling-ts-0.1.0.zip ../sdk-py/dist/cli-tooling-py-0.1.0.zip
go test ./...
go build -ldflags '-X main.version=0.1.0' -o dist/holon ./cmd/holon
```

## Unpublished package artifacts

Create native platform wheels, npm packages, and a binary archive without any
publication:

```sh
python3 scripts/package_cli.py --target darwin_arm64 --version 0.1.0
# Or package a binary built earlier:
python3 scripts/package_cli.py --target darwin_arm64 --version 0.1.0 --binary dist/holon
```

Supported target names are `darwin_arm64`, `darwin_amd64`, `linux_arm64`, and
`linux_amd64`. Every target cross-compiles from any host with `CGO_ENABLED=0`, so
all four can be packaged on one machine; the script still rejects dynamically
linked ELF files. Linux wheels have both manylinux 2.17 and musllinux 1.2 tags.
macOS wheels require macOS 15 or newer. `uv` builds wheels using hatchling, and
`npm pack` creates the launcher and selected platform package.

Each platform build creates `dist/holon_cli-*.whl`, the matching
`dist/holon-cli-<platform>-0.1.0.tgz`, `dist/holon-cli-0.1.0.tgz`, a direct binary
archive, and `SHA256SUMS`. Install the artifacts for your own platform:

```sh
uv venv .venv
uv pip install --no-cache --no-index --find-links dist holon-cli
uv run holon --version
npm install dist/holon-cli-0.1.0.tgz dist/holon-cli-darwin-arm64-0.1.0.tgz
npx holon --version
```

For a Python project's development tools, use
`uv add --dev --find-links /absolute/path/to/dist holon-cli` and
`uv run holon types generate`. CI builds and uploads artifacts for all four
platforms; it never publishes to npm or PyPI. The `@holon` npm scope remains
unverified because no authenticated npm session is available; packages retain
the planned scope until the owner confirms it.
