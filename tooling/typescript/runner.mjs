import vm from "node:vm";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
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
} else throw new Error(`Unknown internal type preparation operation: ${mode}`);
