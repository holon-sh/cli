// A module graph evaluated in a fresh vm context with no host capabilities.
//
// The loader decides what every specifier resolves to: application files from its tree (TypeScript
// transpiled with source maps, `.js` specifiers mapped to `.ts`, tsconfig paths honoured), packages
// through package.json exports with the import/node conditions (CommonJS packages through a shim
// whose require uses the same resolver), and node: builtins as stubs that throw when used. Zod and
// the MCP SDK fall back to the copies bundled with the tooling when the application has none. The
// context has the ECMAScript intrinsics, console, and the web APIs the MCP SDK needs to serve an
// in-memory request; no process, filesystem, network, timers or clock.
import vm from "node:vm";
import module from "node:module";
import { existsSync, readFileSync, realpathSync, statSync } from "node:fs";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";

const schemaPackages = new Set(["zod", "@modelcontextprotocol/server", "@modelcontextprotocol/core"]);
const capabilities = { fs: "filesystem", "fs/promises": "filesystem", net: "network", http: "network", https: "network", http2: "network", dns: "network", "dns/promises": "network", tls: "network", dgram: "network", child_process: "child processes", cluster: "child processes", worker_threads: "child processes", timers: "timers", "timers/promises": "timers", process: "process environment", os: "host information" };
const globalCapabilities = { process: "process environment", fetch: "network", XMLHttpRequest: "network", WebSocket: "network", setTimeout: "timers", setInterval: "timers", setImmediate: "timers", Buffer: "Node.js buffers", require: "require", __dirname: "filesystem", __filename: "filesystem" };
const extensions = ["", ".ts", ".tsx", ".mts", ".js", ".mjs", ".cjs", ".json", "/index.ts", "/index.tsx", "/index.js", "/index.mjs"];

export class MissingPackage extends Error {}

export function createSandbox(root, vendor) {
  root = realpathSync(root);
  const context = vm.createContext({ console });
  for (const name of ["URL", "URLSearchParams", "Headers", "Request", "Response", "ReadableStream", "TextEncoder", "TextDecoder", "AbortController", "AbortSignal"]) context[name] = globalThis[name];
  // A stub fails when called, constructed or read, so the application's stack trace names the line.
  const stub = vm.runInContext(`(name, capability) => {
    const fail = () => { throw new Error(name + " is unavailable: contract evaluation has no " + capability); };
    return new Proxy(function () {}, { apply: fail, construct: fail, get: (_, key) => key === "then" || typeof key === "symbol" ? undefined : fail() });
  }`, context, { filename: "holon:stub" });
  // The clock and randomness are intrinsics; dated values and Date.parse keep working.
  vm.runInContext(`{
    const fail = name => () => { throw new Error(name + " is unavailable: contract evaluation has no " + (name === "Math.random" ? "randomness" : "clock")); };
    globalThis.Date = new Proxy(Date, {
      apply: fail("Date()"),
      construct: (target, args, newTarget) => args.length ? Reflect.construct(target, args, newTarget) : fail("new Date()")(),
      get: (target, key) => key === "now" ? fail("Date.now") : Reflect.get(target, key),
    });
    Math.random = fail("Math.random");
  }`, context, { filename: "holon:stub" });
  const parse = vm.runInContext("JSON.parse", context);
  const modules = new Map(), files = new Map(), commonjs = new Map(), builtins = new Map(), maps = new Map(), served = new Set();
  const paths = tsconfigPaths(root);

  function resolveSpecifier(specifier, referrer, conditions) {
    if (specifier.startsWith("node:") || module.isBuiltin(specifier)) return { builtin: specifier.replace(/^node:/, "") };
    if (/^(\.{1,2}(\/|$)|\/)/.test(specifier) || specifier.startsWith("file:")) {
      const target = specifier.startsWith("file:") ? fileURLToPath(specifier) : resolve(dirname(referrer), specifier);
      return { file: candidate(target) ?? fail(`Cannot find ${specifier} imported from ${referrer}`) };
    }
    if (!referrer.split(sep).includes("node_modules")) {
      for (const target of paths(specifier)) {
        const file = candidate(target);
        if (file) return { file };
      }
    }
    const [, name, subpath] = specifier.match(/^((?:@[^/]+\/)?[^/]+)(\/.*)?$/) ?? fail(`Invalid specifier ${specifier}`);
    for (let directory = dirname(referrer); ; directory = dirname(directory)) {
      const pkg = join(directory, "node_modules", name);
      if (existsSync(join(pkg, "package.json"))) return { file: packageFile(pkg, `.${subpath ?? ""}`, conditions) };
      if (dirname(directory) === directory) break;
    }
    if (schemaPackages.has(name) && existsSync(join(vendor, name, "package.json"))) return { file: packageFile(join(vendor, name), `.${subpath ?? ""}`, conditions) };
    throw new MissingPackage(`Cannot find package ${name} imported from ${referrer}; install the application's dependencies or commit holon-contract.json`, { cause: name });
  }

  function packageFile(pkg, subpath, conditions) {
    const manifest = JSON.parse(readFileSync(join(pkg, "package.json"), "utf8"));
    let target = manifest.exports === undefined ? (subpath === "." ? manifest.main ?? "index.js" : subpath) : exportTarget(manifest.exports, subpath, conditions);
    target ??= fail(`${manifest.name} does not export ${subpath}`);
    return realpathSync(candidate(join(pkg, target)) ?? fail(`Cannot find ${join(pkg, target)}`));
  }

  // load returns the vm module for a file, creating it on first use. Its dependencies link later.
  function load(file) {
    if (modules.has(file)) return modules.get(file);
    const url = pathToFileURL(file).href;
    served.add(file);
    let created;
    const format = formatOf(file);
    if (format === "json") {
      const value = parse(readFileSync(file, "utf8"));
      created = new vm.SyntheticModule(["default"], function () { this.setExport("default", value); }, { identifier: url, context });
    } else if (format === "commonjs") {
      const exports = require(file);
      const names = [...new Set(["default", ...Object.keys(Object(exports))])];
      created = new vm.SyntheticModule(names, function () { for (const name of names) this.setExport(name, name === "default" ? exports : exports[name]); }, { identifier: url, context });
    } else {
      created = new vm.SourceTextModule(source(file, url, ts.ModuleKind.ESNext), {
        identifier: url, context,
        initializeImportMeta: meta => Object.assign(meta, { url, filename: file, dirname: dirname(file) }),
        importModuleDynamically: specifier => {
          const target = resolveSpecifier(specifier, file, ["import", "node"]);
          return evaluate(target.builtin ? builtin(target.builtin) : load(target.file));
        },
      });
    }
    modules.set(file, created);
    files.set(created, file);
    return created;
  }

  function source(file, url, kind) {
    const text = readFileSync(file, "utf8");
    if (!/\.[mc]?tsx?$/.test(file)) return text;
    const { outputText, sourceMapText } = ts.transpileModule(text, { fileName: file, compilerOptions: { module: kind, target: ts.ScriptTarget.ES2022, sourceMap: true, jsx: ts.JsxEmit.ReactJSX, experimentalDecorators: true, esModuleInterop: true } });
    maps.set(url, new module.SourceMap(JSON.parse(sourceMapText)));
    return outputText.replace(/\/\/# sourceMappingURL=.*\s*$/, "");
  }

  // stubs mirrors a builtin's exports with stubs, so imports link and every use fails.
  function stubs(name) {
    if (!builtins.has(name)) {
      const real = module.createRequire(import.meta.url)(`node:${name}`);
      const capability = capabilities[name] ?? `node:${name}`;
      builtins.set(name, Object.fromEntries(Object.keys(real).filter(n => n !== "default").map(n => [n, stub(`node:${name} ${n}`, capability)])));
    }
    return builtins.get(name);
  }

  function builtin(name) {
    const key = `node:${name}`;
    if (!modules.has(key)) {
      const exported = stubs(name);
      const names = ["default", ...Object.keys(exported)];
      modules.set(key, new vm.SyntheticModule(names, function () { for (const n of names) this.setExport(n, n === "default" ? exported : exported[n]); }, { identifier: key, context }));
    }
    return modules.get(key);
  }

  // require evaluates a CommonJS file in the context, resolving its requires through the loader.
  function require(file) {
    if (commonjs.has(file)) return commonjs.get(file).exports;
    served.add(file);
    const record = { exports: {} };
    commonjs.set(file, record);
    if (formatOf(file) === "json") return (record.exports = parse(readFileSync(file, "utf8")));
    const url = pathToFileURL(file).href;
    const fn = vm.compileFunction(source(file, url, ts.ModuleKind.CommonJS), ["exports", "require", "module", "__filename", "__dirname"], { parsingContext: context, filename: url });
    const local = specifier => {
      const target = resolveSpecifier(specifier, file, ["require", "node"]);
      if (target.builtin) return stubs(target.builtin);
      if (formatOf(target.file) === "module") throw new Error(`require() of the ES module ${target.file} is unsupported in contract evaluation`);
      return require(target.file);
    };
    fn.call(record.exports, record.exports, local, record, file, dirname(file));
    return record.exports;
  }

  async function linker(specifier, referencing, { attributes } = {}) {
    const target = resolveSpecifier(specifier, files.get(referencing), ["import", "node"]);
    if (target.builtin) return builtin(target.builtin);
    if (attributes?.type === "json" && formatOf(target.file) !== "json") throw new Error(`${specifier} is not JSON`);
    return load(target.file);
  }

  async function evaluate(created) {
    if (created.status === "unlinked") await created.link(linker);
    if (created.status === "linked") await created.evaluate();
    return created;
  }

  return {
    context,
    served,
    import: async file => (await evaluate(load(realpathSync(file)))).namespace,
    resolve: (specifier, referrer) => resolveSpecifier(specifier, realpathSync(referrer), ["import", "node"]).file,
    // diagnostic rewrites transpiled positions to the original source and names missing capabilities.
    diagnostic(error) {
      let text = String(error?.stack ?? error).replace(/(file:\/\/[^\s):]+):(\d+):(\d+)/g, (match, url, line, column) => {
        const entry = maps.get(url)?.findEntry(line - 1, column - 1);
        const path = fileURLToPath(url);
        return entry?.originalLine === undefined ? `${path}:${line}:${column}` : `${path}:${entry.originalLine + 1}:${entry.originalColumn + 1}`;
      });
      // The loader's own frames (the CommonJS shim, stubs, vm internals) say nothing about the application.
      const tooling = fileURLToPath(import.meta.url);
      text = text.split("\n").filter(line => !line.includes(tooling) && !/holon:stub|node:internal/.test(line)).join("\n");
      const missing = text.match(/^ReferenceError: (\w+) is not defined/);
      if (missing && globalCapabilities[missing[1]]) text = text.replace(missing[0], `${missing[0]}: contract evaluation has no ${globalCapabilities[missing[1]]}`);
      return text;
    },
  };
}

function formatOf(file) {
  if (file.endsWith(".json")) return "json";
  if (/\.(cjs|cts)$/.test(file)) return "commonjs";
  if (/\.(mjs|mts|ts|tsx)$/.test(file)) return "module";
  for (let directory = dirname(file); ; directory = dirname(directory)) {
    const manifest = join(directory, "package.json");
    if (existsSync(manifest)) return JSON.parse(readFileSync(manifest, "utf8")).type === "module" ? "module" : "commonjs";
    if (dirname(directory) === directory) return "commonjs";
  }
}

// candidate is the file a module path names, trying TypeScript sources for .js specifiers.
function candidate(path) {
  const typed = path.replace(/\.(m|c)?js$/, (_, kind) => `.${kind ?? ""}ts`);
  for (const option of [...(typed !== path ? [typed] : []), ...extensions.map(extension => path + extension)]) {
    if (existsSync(option) && statSync(option).isFile()) return realpathSync(option);
  }
}

function exportTarget(exports, subpath, conditions) {
  if (typeof exports === "string" || Array.isArray(exports) || !Object.keys(exports).some(key => key.startsWith("."))) exports = { ".": exports };
  if (Object.hasOwn(exports, subpath)) return conditional(exports[subpath], "", conditions);
  const patterns = Object.keys(exports).filter(key => key.includes("*")).sort((a, b) => b.indexOf("*") - a.indexOf("*"));
  for (const key of patterns) {
    const [prefix, suffix] = key.split("*");
    if (subpath.startsWith(prefix) && subpath.endsWith(suffix) && subpath.length >= key.length - 1) return conditional(exports[key], subpath.slice(prefix.length, subpath.length - suffix.length), conditions);
  }
}

function conditional(value, match, conditions) {
  if (typeof value === "string") return value.replaceAll("*", match);
  if (Array.isArray(value)) {
    for (const option of value) {
      const target = conditional(option, match, conditions);
      if (target !== undefined) return target;
    }
  } else if (value && typeof value === "object") {
    for (const [key, option] of Object.entries(value)) {
      if (key !== "default" && !conditions.includes(key)) continue;
      const target = conditional(option, match, conditions);
      if (target !== undefined) return target;
    }
  }
}

// tsconfigPaths maps a specifier through the application's tsconfig.json paths, if any.
function tsconfigPaths(root) {
  const file = join(root, "tsconfig.json");
  if (!existsSync(file)) return () => [];
  const { config } = ts.readConfigFile(file, ts.sys.readFile);
  const { options } = ts.parseJsonConfigFileContent(config ?? {}, ts.sys, root);
  const base = options.baseUrl ?? options.pathsBasePath ?? root;
  const entries = Object.entries(options.paths ?? {});
  return specifier => entries.flatMap(([pattern, targets]) => {
    const [prefix, suffix = ""] = pattern.split("*");
    const exact = !pattern.includes("*");
    if (exact ? specifier !== pattern : !(specifier.startsWith(prefix) && specifier.endsWith(suffix))) return [];
    const match = exact ? "" : specifier.slice(prefix.length, specifier.length - suffix.length);
    return targets.map(target => resolve(base, target.replace("*", match)));
  });
}

function fail(message) {
  throw new Error(message);
}
