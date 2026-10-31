import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import ts from "typescript";
import { contract, extract } from "./extract.mjs";

const [mode, configPath, output] = process.argv.slice(2);
const config = mode === "contract" ? null : JSON.parse(readFileSync(configPath, "utf8"));
if (mode === "contract") {
  // One application's child process (root, entry). Its timers or sockets must not keep it alive.
  process.send(await contract(configPath, output), () => process.exit(0));
} else if (mode === "extract") {
  const results = Object.fromEntries(await Promise.all(config.apps.map(async app => [app.name, await extract(app.path, app.entry)])));
  writeFileSync(output, JSON.stringify(results));
} else if (mode === "check" || mode === "verify") {
  const root = resolve(config.root ?? ".");
  for (const [path, expected] of Object.entries(config.sources ?? {})) {
    const actual = createHash("sha256").update(readFileSync(join(root, path))).digest("hex");
    if (actual !== expected) throw new Error(`${path}: application install/build changed a source file used for its MCP contract; refusing to publish stale types.`);
  }
  if (mode === "verify") process.exit(0);
  const require = createRequire(join(root, "package.json"));
  const compilerPackage = require.resolve("typescript/package.json");
  const tsconfig = join(root, "tsconfig.json");
  const flags = existsSync(tsconfig)
    ? ["--noEmit", "--project", tsconfig]
    : ["--noEmit", "--strict", "--skipLibCheck", "--allowJs", "--target", "ES2023", "--module", "NodeNext", "--moduleResolution", "NodeNext", "--allowImportingTsExtensions",
      ...(config.entry ? [join(root, config.entry)] : ts.sys.readDirectory(root, [".ts", ".mts", ".js", ".mjs"], ["node_modules", ".holon", ".holon-types"]))];
  const compiler = join(dirname(compilerPackage), JSON.parse(readFileSync(compilerPackage, "utf8")).bin.tsc);
  const result = spawnSync(process.execPath, [compiler, ...flags], { cwd: root, stdio: "inherit" });
  if (result.error) throw result.error;
  process.exitCode = result.status ?? 1;
} else throw new Error(`Unknown internal type preparation operation: ${mode}`);
