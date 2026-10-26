# Embedded tooling

Go owns extraction orchestration, fingerprints, contract validation and source
verification in `pkg/tooling/core`. `pkg/typegen` calls that library directly;
its `Options.Extract` hook still supports extraction inside language images.
Applications are statically analyzed without importing their SDKs or executing
initialization code.

The language workers retain their language semantics:

- TypeScript uses the TypeScript AST, Zod's JSON Schema conversion and Ajv. Go
  selects the schemas to compile; Ajv preserves JavaScript regexes (including
  lookaheads) and draft semantics. Checking uses the application's TypeScript
  compiler library when installed, otherwise the bundled compiler/declarations.
- Python uses the standard-library AST. Go fingerprints all Python source files,
  respecting the frontend's import exclusions, and invokes mypy in the selected
  application interpreter. AST source bytes are preserved, including CRLF.

Both frontends return the source text they analyzed. Go hashes it and verifies
that files still match before publishing a manifest. A later `check` or `verify`
uses the same verification function; missing and changed files fail closed.
The existing snapshot fingerprint also continues to guard source changes during
preparation, including modes, sizes and symlink targets.

## Embedding and compatibility

After `tooling.Unpack(directory)`, Go hosts can use typed library calls:

```go
runner := core.Runner{
    Language: "typescript", Program: node,
    Path: filepath.Join(directory, "typescript", "runner.mjs"),
    Stdout: stdout, Stderr: stderr,
}
results, err := runner.Extract(ctx, []core.App{{Name: "orders", Path: source, Entry: "server.ts"}})
// After installing the app's dependencies, check before building:
err = runner.Check(ctx, core.CheckConfig{
    Root: source, Entry: "server.ts", Sources: results["orders"].Sources, Check: true,
}, false)
// After the build, verify the same manifest without invoking a compiler:
err = core.Verify(source, results["orders"].Sources)
```

Existing `runner.mjs` and `runner.py` entrypoints keep their `extract`, `check`
and `verify` modes and JSON configurations. They delegate to a bundled
`holon-tooling` executable linked against the same Go library, so image builds
need neither a Go installation nor changes to `Prepared.CheckConfig` or their
commands. `tooling.Unpack` supplies `typescript/`, `python/` and `core/` together.
When unpacking archives manually, include the common archive as `core/` beside
the language directories. The `analyze`, `schemas` and `compile` operations are
internal worker operations, not substitutes for the validated public entrypoints.

## Rebuilding

`scripts/build-tooling` rebuilds the three archives and `pkg/tooling/SHA256SUMS`.
The archives are committed; CI rebuilds them and fails when they differ.

```sh
(cd tooling/typescript && npm ci && npm test)
(cd tooling/python && uv sync && uv run pytest && uv run mypy --strict)
scripts/build-tooling
go test ./...
```

The native host is built with Go 1.27.2, CGO disabled, trimmed paths and no VCS or
build ID, for Linux/macOS on amd64/arm64. This deliberately pins the compiler for
reproducibility. Language archives bundle their dependencies and standard
TypeScript declarations as before; no Go toolchain is required at runtime.
