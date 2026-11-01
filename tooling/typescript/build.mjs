// Bundle the standalone extractor and check runner into the archive pkg/tooling embeds.
// Type checking uses the consumer's installed compiler and standard declarations.
// The output is byte-for-byte reproducible from package-lock.json.
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { zipSync } from "fflate";

const root = dirname(fileURLToPath(import.meta.url));
const { version } = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
const destination = join(root, "..", "..", "pkg", "tooling");
const nodeBanner = 'import { createRequire as _createRequire } from "node:module"; import { fileURLToPath as _fileURLToPath } from "node:url"; import { dirname as _dirname } from "node:path"; const require = _createRequire(import.meta.url); const __filename = _fileURLToPath(import.meta.url); const __dirname = _dirname(__filename);';
const { metafile, outputFiles } = await build({ entryPoints: [join(root, "runner.mjs")], bundle: true, write: false, metafile: true, format: "esm", legalComments: "inline", absWorkingDir: root, platform: "node", target: "node22", banner: { js: nodeBanner }, outfile: "runner.mjs" });

// Zod and the MCP SDK ship as published ES modules for applications that have not installed them.
const vendored = ["zod", "@modelcontextprotocol/server", "@modelcontextprotocol/core"].map(name => join(root, "node_modules", name));
const packages = new Set(vendored);
for (const path of Object.keys(metafile.inputs)) {
  if (!path.includes("node_modules/")) continue;
  let directory = dirname(join(root, path));
  while (!existsSync(join(directory, "package.json"))) directory = dirname(directory);
  packages.add(directory);
}
const notices = [];
for (const directory of [...packages].sort()) {
  const pkg = JSON.parse(readFileSync(join(directory, "package.json"), "utf8"));
  const licensesIn = directory => readdirSync(directory).filter(f => /^(licen[sc]e|notice)(\.|$)/i.test(f));
  let licenseDirectory = directory;
  let licenses = licensesIn(directory);
  while (!licenses.length && licenseDirectory.includes("node_modules/")) { licenseDirectory = dirname(licenseDirectory); licenses = licensesIn(licenseDirectory); }
  if (!licenses.length) throw new Error(`Missing license notice for ${relative(root, directory)}`);
  notices.push(`${pkg.name ?? relative(root, directory)} ${pkg.version ?? ""}\n${licenses.sort().map(name => readFileSync(join(licenseDirectory, name), "utf8")).join("\n")}`);
}

const files = { "runner.mjs": outputFiles[0].contents, "THIRD-PARTY-NOTICES.txt": Buffer.from(notices.join("\n\n")), "version.json": Buffer.from(JSON.stringify({ version }) + "\n") };
for (const directory of vendored) {
  for (const file of readdirSync(directory, { recursive: true })) {
    if (/(^|\/)(package\.json|[^/]*\.m?js|licen[sc]e[^/]*)$/i.test(file) && !file.startsWith("src/")) files[`vendor/${relative(root, directory)}/${file}`] = readFileSync(join(directory, file));
  }
}
const bytes = zipSync(Object.fromEntries(Object.entries(files).sort(([a], [b]) => a.localeCompare(b)).map(([name, data]) => [name, [new Uint8Array(data), { mtime: new Date(1980, 0, 1) }]])), { level: 9 });
mkdirSync(destination, { recursive: true });
for (const name of readdirSync(destination)) if (/^cli-tooling-ts-.*\.zip$/.test(name)) rmSync(join(destination, name));
const archive = join(destination, `cli-tooling-ts-${version}.zip`);
writeFileSync(archive, bytes);
console.log(relative(process.cwd(), archive));
