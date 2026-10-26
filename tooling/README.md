# Embedded tooling

The CLI analyzes applications with language-specific tools that it carries as
archives in `pkg/tooling` and unpacks beside the application at run time. This
directory holds their sources; the public SDKs are runtime-only.

- `typescript/`: `extract.mjs` statically extracts an MCP handler's tool and
  resource contracts; `runner.mjs` wraps it with the `extract`, `check`, and
  `verify` modes. CLI commands invoke `extract` and `check`; platform also uses
  `verify` through the embedded runner. `build.mjs` bundles it with esbuild,
  adds the TypeScript standard declarations under `lib/`, and writes
  `pkg/tooling/cli-tooling-ts-<version>.zip`.
- `python/`: `extract.py` is the same extractor for Python applications and
  runs on any Python 3.11+ without site-packages; `runner.py` is the `check`
  and `verify` runner, which imports mypy from the application's environment.
  `build.py` writes `pkg/tooling/cli-tooling-py-<version>.zip`.

`scripts/build-tooling` rebuilds both archives and `pkg/tooling/SHA256SUMS`.
The archives are committed; CI rebuilds them and fails when they differ.

```sh
(cd tooling/typescript && npm ci && npm test)
(cd tooling/python && uv sync && uv run pytest && uv run mypy --strict)
scripts/build-tooling
```

Both builds are deterministic: the TypeScript archive depends only on
`package-lock.json`, and the Python archive contains the two source files.
