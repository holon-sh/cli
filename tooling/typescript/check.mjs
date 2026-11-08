// Type-check an application against its typed contracts with its own TypeScript compiler.
//
// Usage: check.mjs check ENTRY, in the application's directory; an empty ENTRY checks all of it. It
// imports only Node.js, so a check does not wait for the extractor's bundled compiler to load.
import { spawnSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";

const root = process.cwd();
const entry = process.argv[3];
const generated = new Set(["node_modules", ".holon", ".holon-types"]);

// sources lists the project's scripts outside the top-level installed and generated directories. An
// unreadable directory has none, and symbolic links are not followed.
function sources(directory) {
  let entries = [];
  try {
    entries = readdirSync(directory, { withFileTypes: true });
  } catch {}
  return entries.flatMap(item => {
    const path = join(directory, item.name);
    if (item.isDirectory()) return directory === root && generated.has(item.name) ? [] : sources(path);
    return item.isFile() && /\.m?[jt]s$/.test(item.name) ? [path] : [];
  });
}

const compilerPackage = createRequire(join(root, "package.json")).resolve("typescript/package.json");
const tsconfig = join(root, "tsconfig.json");
const flags = existsSync(tsconfig)
  ? ["--noEmit", "--project", tsconfig]
  : ["--noEmit", "--strict", "--skipLibCheck", "--allowJs", "--target", "ES2023", "--module", "NodeNext", "--moduleResolution", "NodeNext", "--allowImportingTsExtensions",
    ...(entry ? [join(root, entry)] : sources(root).sort())];
const compiler = join(dirname(compilerPackage), JSON.parse(readFileSync(compilerPackage, "utf8")).bin.tsc);
const result = spawnSync(process.execPath, [compiler, ...flags], { cwd: root, stdio: "inherit" });
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
