import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, symlinkSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { spawnSync } from "node:child_process";
import { unzipSync } from "fflate";

const { version } = JSON.parse(readFileSync(resolve("package.json"), "utf8"));
const archive = resolve("..", "..", "pkg", "tooling", `cli-tooling-ts-${version}.zip`);

// The archive is what the CLI embeds: unpack it exactly as pkg/tooling does.
function unpackTooling(t) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "holon-tooling-")));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const tooling = join(root, "typescript");
  for (const [name, bytes] of Object.entries(unzipSync(readFileSync(archive)))) {
    assert.match(name, /^(runner\.mjs|version\.json|THIRD-PARTY-NOTICES\.txt|lib\/lib.*\.d\.ts)$/);
    mkdirSync(dirname(join(tooling, name)), { recursive: true });
    writeFileSync(join(tooling, name), bytes);
  }
  for (const [name, bytes] of Object.entries(unzipSync(readFileSync(resolve("..", "..", "pkg", "tooling", "cli-tooling-core.zip"))))) {
    mkdirSync(join(root, "core"), { recursive: true });
    writeFileSync(join(root, "core", name), bytes, { mode: 0o755 });
  }
  const run = (mode, config, output = []) => spawnSync(process.execPath, [join(tooling, "runner.mjs"), mode, config, ...output], { encoding: "utf8" });
  return { root, run };
}

test("check uses bundled declarations or the application's compiler", t => {
  const { root, run } = unpackTooling(t);
  const app = join(root, "application");
  mkdirSync(app);
  writeFileSync(join(app, "package.json"), '{"type":"module"}');
  const source = "export const total: number = [1, 2].length;\nexport const label = total.toFixed();\n";
  writeFileSync(join(app, "check.ts"), source);
  const config = join(root, "check.json");
  writeFileSync(config, JSON.stringify({ root: app, entry: "check.ts", sources: {} }));
  const bundled = run("check", config);
  assert.equal(bundled.status, 0, bundled.stderr + bundled.stdout);
  writeFileSync(join(app, "check.ts"), source + "export const wrong: string = total;\n");
  const typeError = run("check", config);
  assert.notEqual(typeError.status, 0);
  assert.match(typeError.stderr, /TS2322/);
  writeFileSync(join(app, "check.ts"), source);
  mkdirSync(join(app, "node_modules"));
  symlinkSync(resolve("node_modules/typescript"), join(app, "node_modules/typescript"));
  writeFileSync(join(app, "tsconfig.json"), JSON.stringify({ compilerOptions: { target: "ES2023", module: "ESNext", moduleResolution: "Bundler", strict: true }, files: ["check.ts"] }));
  const configured = run("check", config);
  assert.equal(configured.status, 0, configured.stderr + configured.stdout);
  writeFileSync(join(app, "check.ts"), source + "export const wrong: string = total;\n");
  const configuredError = run("check", config);
  assert.notEqual(configuredError.status, 0);
  assert.match(configuredError.stdout + configuredError.stderr, /TS2322/);
});
