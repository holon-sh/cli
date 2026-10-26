#!/usr/bin/env node
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import { delimiter, dirname } from "node:path";

try {
  const binary = process.env.HOLON_BINARY || createRequire(import.meta.url).resolve(`holon-types-${process.platform}-${process.arch}/bin/holon`);
  const env = { ...process.env, PATH: `${dirname(process.execPath)}${delimiter}${process.env.PATH ?? ""}` };
  const child = spawnSync(binary, process.argv.slice(2), { stdio: "inherit", env });
  if (child.error) throw child.error;
  if (child.signal) process.kill(process.pid, child.signal);
  process.exit(child.status ?? 1);
} catch (error) {
  console.error(`holon: ${error.message}; reinstall holon-types with optional dependencies enabled`);
  process.exit(1);
}
