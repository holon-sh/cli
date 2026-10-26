import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { createRequire } from "node:module";
import { createHash } from "node:crypto";
import bundledTs from "typescript";
import { extract } from "./extract.mjs";

const [mode, configPath, output] = process.argv.slice(2);
const config = JSON.parse(readFileSync(configPath, "utf8"));
const assets = dirname(process.argv[1]);
if (mode === "extract") {
  const results = Object.fromEntries(await Promise.all(config.apps.map(async app => [app.name, await extract(app.path, app.entry)])));
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
  try { compiler = require.resolve("typescript"); } catch {}
  const ts = compiler ? require(compiler) : bundledTs;
  process.chdir(root);
  const tsconfig = existsSync(join(root, "tsconfig.json")) ? join(root, "tsconfig.json") : undefined;
  const parsed = tsconfig ? ts.getParsedCommandLineOfConfigFile(tsconfig, { noEmit: true }, { ...ts.sys, onUnRecoverableConfigFileDiagnostic: d => { throw new Error(ts.flattenDiagnosticMessageText(d.messageText, "\n")); } }) : {
    options: { noEmit: true, strict: true, skipLibCheck: true, allowJs: true, target: ts.ScriptTarget.ES2023, module: ts.ModuleKind.NodeNext, moduleResolution: ts.ModuleResolutionKind.NodeNext, allowImportingTsExtensions: true },
    fileNames: config.entry ? [join(root, config.entry)] : ts.sys.readDirectory(root, [".ts", ".mts", ".js", ".mjs"], ["node_modules", ".holon", ".holon-types"]),
  };
  const host = ts.createCompilerHost(parsed.options);
  if (!compiler) {
    host.getDefaultLibFileName = () => join(assets, "lib", ts.getDefaultLibFileName(parsed.options));
    host.getDefaultLibLocation = () => join(assets, "lib");
  }
  const program = ts.createProgram({ rootNames: parsed.fileNames, options: parsed.options, projectReferences: parsed.projectReferences, configFileParsingDiagnostics: parsed.errors, host });
  const diagnostics = ts.getPreEmitDiagnostics(program);
  if (diagnostics.length) {
    process.stderr.write(ts.formatDiagnostics(diagnostics, host));
    process.exitCode = 1;
  }
} else throw new Error(`Unknown internal type preparation operation: ${mode}`);
