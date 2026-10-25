import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import ts from "typescript";
import { clientUsage, extract } from "./extract.mjs";

const [mode, configPath, output] = process.argv.slice(2);
const config = JSON.parse(readFileSync(configPath, "utf8"));
const assets = dirname(process.argv[1]);
if (mode === "clients") {
  writeFileSync(output, JSON.stringify(Object.fromEntries(Object.entries(config).map(([name, paths]) => [name, clientUsage(paths)]))));
} else if (mode === "extract") {
  const results = Object.fromEntries(config.apps.map(app => [app.name, extract(app.path, app.entry)]));
  writeFileSync(output, JSON.stringify(results));
} else if (mode === "check" || mode === "verify") {
  const root = resolve(config.root ?? ".");
  for (const [path, expected] of Object.entries(config.sources ?? {})) {
    const actual = createHash("sha256").update(readFileSync(join(root, path))).digest("hex");
    if (actual !== expected) throw new Error(`${path}: application install/build changed a source file used for its MCP contract; refusing to publish stale types.`);
  }
  if (mode === "verify") process.exit(0);
  const require = createRequire(join(root, "package.json"));
  let compiler;
  try { compiler = require.resolve("typescript/bin/tsc"); } catch {}
  const tsconfig = existsSync(join(root, "tsconfig.json")) ? join(root, "tsconfig.json") : undefined;
  const sources = config.entry ? [join(root, config.entry)] : ts.sys.readDirectory(root, [".ts", ".mts", ".js", ".mjs"], ["node_modules", ".holon", ".holon-types"]);
  if (compiler) {
    const flags = tsconfig ? ["--noEmit", "--project", tsconfig] : ["--noEmit", "--strict", "--skipLibCheck", "--allowJs", "--target", "ES2023", "--module", "NodeNext", "--moduleResolution", "NodeNext", "--allowImportingTsExtensions", ...sources];
    const result = spawnSync(process.execPath, [compiler, ...flags], { cwd: root, stdio: "inherit" });
    if (result.error) throw result.error;
    process.exitCode = result.status ?? 1;
  } else {
    let options = { noEmit: true, strict: true, skipLibCheck: true, allowJs: true, target: ts.ScriptTarget.ES2023, module: ts.ModuleKind.NodeNext, moduleResolution: ts.ModuleResolutionKind.NodeNext, allowImportingTsExtensions: true };
    let files = sources;
    let configurationErrors = [];
    if (tsconfig) {
      const parsed = ts.getParsedCommandLineOfConfigFile(tsconfig, { noEmit: true }, { ...ts.sys, onUnRecoverableConfigFileDiagnostic: d => { throw new Error(ts.flattenDiagnosticMessageText(d.messageText, "\n")); } });
      options = parsed.options;
      files = parsed.fileNames;
      configurationErrors = parsed.errors;
    }
    const host = ts.createCompilerHost(options);
    host.getDefaultLibFileName = () => join(assets, "lib", ts.getDefaultLibFileName(options));
    host.getDefaultLibLocation = () => join(assets, "lib");
    const program = ts.createProgram(files, options, host);
    const diagnostics = [...configurationErrors, ...ts.getPreEmitDiagnostics(program)];
    if (diagnostics.length) {
      process.stderr.write(ts.formatDiagnostics(diagnostics, { getCurrentDirectory: () => root, getCanonicalFileName: p => p, getNewLine: () => "\n" }));
      process.exitCode = 1;
    }
  }
} else throw new Error(`Unknown internal type preparation operation: ${mode}`);
