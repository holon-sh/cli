#!/usr/bin/env node
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";

const { platform, arch } = process;
if (!["darwin", "linux"].includes(platform) || !["arm64", "x64"].includes(arch)) {
  console.error(`holon: unsupported platform ${platform}-${arch}`);
  process.exit(1);
}
const name = `@holon/cli-${platform}-${arch}`;
let binary;
try {
  binary = createRequire(import.meta.url).resolve(`${name}/bin/holon`);
} catch {
  console.error(`holon: missing ${name}; reinstall @holon/cli with optional dependencies enabled`);
  process.exit(1);
}
const child = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });
if (child.error) {
  console.error(`holon: ${child.error.message}`);
  process.exit(1);
}
if (child.signal) process.kill(process.pid, child.signal);
process.exit(child.status ?? 1);
