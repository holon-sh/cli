#!/usr/bin/env node
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import { delimiter, dirname } from "node:path";

const platforms = { darwin: "darwin", linux: "linux" };
const cpus = { arm64: "arm64", x64: "x64" };

function locate() {
  if (process.env.HOLON_BINARY) return process.env.HOLON_BINARY;
  const os = platforms[process.platform], cpu = cpus[process.arch];
  if (!os || !cpu) fail(`unsupported platform ${process.platform}-${process.arch}; macOS and Linux on arm64 and x86-64 are supported`);
  const name = `holon-types-${os}-${cpu}`;
  try {
    return createRequire(import.meta.url).resolve(`${name}/bin/holon`);
  } catch {
    fail(`${name} is not installed; reinstall holon-types with optional dependencies enabled`);
  }
}

function fail(message) {
  console.error(`holon: ${message}`);
  process.exit(1);
}

const env = { ...process.env, PATH: `${dirname(process.execPath)}${delimiter}${process.env.PATH ?? ""}` };
const child = spawnSync(locate(), process.argv.slice(2), { stdio: "inherit", env });
if (child.error) fail(child.error.message);
if (child.signal) process.kill(process.pid, child.signal);
process.exit(child.status ?? 1);
