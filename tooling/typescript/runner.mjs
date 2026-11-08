import vm from "node:vm";
import { readFileSync, existsSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import ts from "typescript";
import { contract } from "./extract.mjs";

const [mode, ...args] = process.argv.slice(2);
if (mode === "contract" && !vm.SourceTextModule) {
  // contract ROOT ENTRY evaluates modules in a vm context, which needs --experimental-vm-modules.
  const { status } = spawnSync(process.execPath, ["--experimental-vm-modules", "--no-warnings", ...process.argv.slice(1)], { stdio: "inherit" });
  process.exit(status ?? 1);
} else if (mode === "contract") {
  // Dates render in UTC, as on every machine. The contract goes to stdout; a diagnostic to stderr
  // with status 2, or 3 naming the uninstalled package last.
  process.env.TZ = "UTC";
  const result = await contract(resolve(args[0]), args[1]);
  if (result.diagnostic) process.stderr.write(`${result.diagnostic}\n${result.missing ? `${result.missing}\n` : ""}`, () => process.exit(result.missing ? 3 : 2));
  else process.stdout.write(JSON.stringify(result), () => process.exit(0));
} else if (mode === "check") {
  const config = JSON.parse(readFileSync(args[0], "utf8"));
  const root = resolve(config.root ?? ".");
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
