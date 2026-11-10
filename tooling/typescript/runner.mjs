import vm from "node:vm";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { serve } from "./extract.mjs";

const [mode, ...args] = process.argv.slice(2);
if (mode === "serve" && !vm.SourceTextModule) {
  // serve ROOT ENTRY evaluates modules in a vm context, which needs --experimental-vm-modules.
  const { status } = spawnSync(process.execPath, ["--experimental-vm-modules", "--no-warnings", ...process.argv.slice(1)], { stdio: "inherit" });
  process.exit(status ?? 1);
} else if (mode === "serve") {
  // Dates render in UTC, as on every machine. The server speaks MCP on stdin and stdout; a failure is
  // a diagnostic on stderr with status 2, or 3 naming the uninstalled package last.
  process.env.TZ = "UTC";
  const failure = await serve(resolve(args[0]), args[1]);
  if (failure) process.stderr.write(`${failure.diagnostic}\n${failure.missing ? `${failure.missing}\n` : ""}`, () => process.exit(failure.missing ? 3 : 2));
  else process.stdout.write("", () => process.exit(0));
} else throw new Error(`Unknown internal type preparation operation: ${mode}`);
