// Type-check an application against its typed contracts with its own TypeScript compiler.
//
// Usage: check.mjs check ENTRY, in the application's directory; an empty ENTRY checks all of it. It
// imports only Node.js, so a check does not wait for the extractor's bundled compiler to load.
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";

const root = process.cwd();
const entry = process.argv[3];
const compilerPackage = createRequire(join(root, "package.json")).resolve("typescript/package.json");
// tsc finds the project's tsconfig.json itself; without one it compiles only the files it is named.
const flags = ["--noEmit"];
if (!hasConfig(root)) {
  if (!entry) {
    console.error(`${root}: no tsconfig.json here or in a parent directory; checking a project needs one.`);
    process.exit(1);
  }
  flags.push("--strict", "--skipLibCheck", "--allowJs", "--target", "ES2023", "--module", "NodeNext", "--moduleResolution", "NodeNext", "--allowImportingTsExtensions", join(root, entry));
}
const compiler = join(dirname(compilerPackage), JSON.parse(readFileSync(compilerPackage, "utf8")).bin.tsc);
const result = spawnSync(process.execPath, [compiler, ...flags], { cwd: root, stdio: "inherit" });
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;

// hasConfig reports whether tsc, started in directory, finds a tsconfig.json there or in a parent.
function hasConfig(directory) {
  return existsSync(join(directory, "tsconfig.json")) || (dirname(directory) !== directory && hasConfig(dirname(directory)));
}
