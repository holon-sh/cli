// Check an application against its extracted sources and typed contracts.
//
// Usage: check.mjs check|verify CONFIG. The config ({root, entry, sources}) maps source paths to the
// SHA-256 digests recorded at extraction. verify stops once the sources match; check then runs the
// application's own TypeScript compiler. The checker loads no TypeScript itself, so it starts at once.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, realpathSync, statSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

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
  const tsconfig = join(root, "tsconfig.json");
  const flags = existsSync(tsconfig)
    ? ["--noEmit", "--project", tsconfig]
    : ["--noEmit", "--strict", "--skipLibCheck", "--allowJs", "--target", "ES2023", "--module", "NodeNext", "--moduleResolution", "NodeNext", "--allowImportingTsExtensions",
      ...(config.entry ? [join(root, config.entry)] : sources(root))];
  const compiler = join(dirname(compilerPackage), JSON.parse(readFileSync(compilerPackage, "utf8")).bin.tsc);
  const result = spawnSync(process.execPath, [compiler, ...flags], { cwd: root, stdio: "inherit" });
  if (result.error) throw result.error;
  process.exitCode = result.status ?? 1;
}

// sources lists the project's .ts, .mts, .js and .mjs files as TypeScript's sys.readDirectory does,
// without the top-level node_modules, .holon and .holon-types: each directory's files before its
// subdirectories, both in code-unit order, following each symbolic link to a directory once. Names
// compare as the file system does, which is case-insensitive when this file is found by another case.
function sources(root) {
  const here = fileURLToPath(import.meta.url);
  const fold = existsSync(here.replace(/\w/g, c => (c === c.toUpperCase() ? c.toLowerCase() : c.toUpperCase()))) ? name => name.toLowerCase() : name => name;
  const skipped = new Set(["node_modules", ".holon", ".holon-types"]);
  const visited = new Set();
  const files = [];
  const visit = (directory, top) => {
    let real = directory;
    try {
      real = realpathSync.native(directory);
    } catch {}
    if (visited.has(fold(real))) return;
    visited.add(fold(real));
    let entries = [];
    try {
      entries = readdirSync(directory, { withFileTypes: true });
    } catch {}
    const names = { files: [], directories: [] };
    for (const entry of entries) {
      let kind = entry;
      if (entry.isSymbolicLink()) {
        try {
          kind = statSync(join(directory, entry.name));
        } catch {
          continue;
        }
      }
      if (kind.isFile()) names.files.push(entry.name);
      else if (kind.isDirectory()) names.directories.push(entry.name);
    }
    for (const name of names.files.sort()) if (/\.m?[jt]s$/.test(name)) files.push(join(directory, name));
    for (const name of names.directories.sort()) if (!top || !skipped.has(fold(name))) visit(join(directory, name), false);
  };
  visit(root, true);
  return files;
}
