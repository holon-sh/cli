// Check an application against its extracted sources and typed contracts.
//
// Usage: check.mjs check|verify CONFIG. The config ({root, entry, sources}) maps source paths to the
// SHA-256 digests recorded at extraction. verify stops once the sources match; check then runs the
// application's own TypeScript compiler. The checker loads no TypeScript itself, so it starts at once.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";

const [mode, path] = process.argv.slice(2);
if (mode !== "check" && mode !== "verify") throw new Error(`Unknown internal type preparation operation: ${mode}`);
const config = JSON.parse(readFileSync(path, "utf8"));
const root = resolve(config.root ?? ".");
for (const [file, expected] of Object.entries(config.sources ?? {})) {
  const actual = createHash("sha256").update(readFileSync(join(root, file))).digest("hex");
  if (actual !== expected) throw new Error(`${file}: application install/build changed a source file used for its MCP contract; refusing to publish stale types.`);
}
if (mode === "check") {
  const compilerPackage = createRequire(join(root, "package.json")).resolve("typescript/package.json");
  // tsc finds the project's tsconfig.json itself; without one it compiles only the files it is named.
  const flags = ["--noEmit"];
  if (!hasConfig(root)) {
    if (!config.entry) {
      console.error(`${root}: no tsconfig.json here or in a parent directory; checking a project needs one.`);
      process.exit(1);
    }
    flags.push("--strict", "--skipLibCheck", "--allowJs", "--target", "ES2023", "--module", "NodeNext", "--moduleResolution", "NodeNext", "--allowImportingTsExtensions", join(root, config.entry));
  }
  const compiler = join(dirname(compilerPackage), JSON.parse(readFileSync(compilerPackage, "utf8")).bin.tsc);
  const result = spawnSync(process.execPath, [compiler, ...flags], { cwd: root, stdio: "inherit" });
  if (result.error) throw result.error;
  process.exitCode = result.status ?? 1;
}

// hasConfig reports whether tsc, started in directory, finds a tsconfig.json there or in a parent.
function hasConfig(directory) {
  return existsSync(join(directory, "tsconfig.json")) || (dirname(directory) !== directory && hasConfig(dirname(directory)));
}
