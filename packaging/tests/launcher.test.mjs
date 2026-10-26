import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, copyFileSync, mkdtempSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, test } from "node:test";

const directory = realpathSync(mkdtempSync(join(tmpdir(), "holon launcher ")));
const binary = join(directory, "fake holon");
copyFileSync(new URL("fake-holon.sh", import.meta.url), binary);
chmodSync(binary, 0o755);
after(() => rmSync(directory, { recursive: true, force: true }));
const launcher = fileURLToPath(new URL("../npm/launcher/bin/holon.js", import.meta.url));

function launch(args = [], env = {}) {
  return spawnSync(process.execPath, [launcher, ...args], {
    cwd: directory,
    env: {
      ...process.env, HOLON_BINARY: binary, FROM_ENV: "inherited value",
      VIRTUAL_ENV: "/project/.venv", PATH: "/usr/bin:/bin", EXIT_WITH: "0", DIE_WITH: "", ...env,
    },
    input: "input from caller\n", encoding: "utf8", timeout: 10000,
  });
}

test("preserves arguments, status, stdio, cwd and environment; selects the running Node", () => {
  for (const status of [0, 3, 17]) {
    const result = launch(["--version", "-s", "a=b", "", "two words"], { EXIT_WITH: String(status) });
    assert.equal(result.status, status);
    assert.equal(result.stdout, "<--version>\n<-s>\n<a=b>\n<>\n<two words>\n" +
      `env=inherited value\ncwd=${directory}\npath=${dirname(process.execPath)}${delimiter}/usr/bin:/bin\n` +
      "venv=/project/.venv\nstdin=input from caller\n");
    assert.equal(result.stderr, "child stderr\n");
  }
});

test("exits by the child's signal", () => {
  const result = launch([], { DIE_WITH: "TERM" });
  assert.equal(result.status, null);
  assert.equal(result.signal, "SIGTERM");
});

test("reports a missing executable", () => {
  const result = launch([], { HOLON_BINARY: join(directory, "missing") });
  assert.equal(result.status, 1);
  assert.equal(result.stdout, "");
  assert.match(result.stderr, /^holon: /);
});

test("names the missing platform package when no binary override is given", () => {
  for (const override of ["", undefined]) {
    const result = launch([], { HOLON_BINARY: override });
    assert.equal(result.status, 1);
    assert.equal(result.stdout, "");
    assert.match(result.stderr, /^holon: /);
    assert.ok(result.stderr.includes(`holon-types-${process.platform}-${process.arch}`));
    assert.match(result.stderr, /reinstall holon-types with optional dependencies enabled/);
  }
});
