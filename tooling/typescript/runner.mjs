import { readFileSync, existsSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import ts from "typescript";
import { extract } from "./extract.mjs";
import { validator } from "./validate.mjs";

const [mode, ...args] = process.argv.slice(2);
const assets = dirname(process.argv[1]);
if (mode === "analyze") {
  process.stdout.write(JSON.stringify(extract(...args)));
} else if (mode === "schemas") {
  for (const schema of JSON.parse(readFileSync(0, "utf8"))) validator(schema);
} else if (mode === "compile") {
  const config = JSON.parse(readFileSync(0, "utf8"));
  const root = resolve(config.root);
  const require = createRequire(join(root, "package.json"));
  let compiler = ts, lib = join(assets, "lib");
  try { compiler = require("typescript"); lib = dirname(require.resolve("typescript")); } catch {}
  const tsconfig = existsSync(join(root, "tsconfig.json")) ? join(root, "tsconfig.json") : undefined;
  const sources = config.entry ? [join(root, config.entry)] : compiler.sys.readDirectory(root, [".ts", ".mts", ".js", ".mjs"], ["node_modules", ".holon", ".holon-types"]);
  let options = { noEmit: true, strict: true, skipLibCheck: true, allowJs: true, target: compiler.ScriptTarget.ES2023, module: compiler.ModuleKind.NodeNext, moduleResolution: compiler.ModuleResolutionKind.NodeNext, allowImportingTsExtensions: true };
  let files = sources;
  let configurationErrors = [];
  if (tsconfig) {
    const parsed = compiler.getParsedCommandLineOfConfigFile(tsconfig, { noEmit: true }, { ...compiler.sys, onUnRecoverableConfigFileDiagnostic: d => { throw new Error(compiler.flattenDiagnosticMessageText(d.messageText, "\n")); } });
    options = parsed.options;
    files = parsed.fileNames;
    configurationErrors = parsed.errors;
  }
  const host = compiler.createCompilerHost(options);
  host.getDefaultLibFileName = () => join(lib, compiler.getDefaultLibFileName(options));
  host.getDefaultLibLocation = () => lib;
  const program = compiler.createProgram(files, options, host);
  const diagnostics = [...configurationErrors, ...compiler.getPreEmitDiagnostics(program)];
  if (diagnostics.length) {
    process.stderr.write(compiler.formatDiagnostics(diagnostics, { getCurrentDirectory: () => root, getCanonicalFileName: p => p, getNewLine: () => "\n" }));
    process.exitCode = 1;
  }
} else {
  const core = join(assets, "..", "core", `${process.platform}-${process.arch === "x64" ? "amd64" : process.arch}`);
  const result = spawnSync(core, ["typescript", process.execPath, process.argv[1], mode, ...args], { stdio: "inherit" });
  if (result.error) throw result.error;
  process.exitCode = result.status ?? 1;
}
