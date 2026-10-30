import ts from "typescript";
import { readFileSync, realpathSync, existsSync } from "node:fs";
import { resolve, relative, sep, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";

// Inspect the consumer's type program. Never import its server or run a callback.
export function functionApp(root, entry, descriptor) {
  const filename = resolve(root, entry);
  if (!readFileSync(filename, "utf8").includes("holon-sh/app")) return;
  const configPath = ts.findConfigFile(root, ts.sys.fileExists);
  const options = configPath
    ? ts.parseJsonConfigFileContent(
        ts.readConfigFile(configPath, ts.sys.readFile).config,
        ts.sys,
        root,
      ).options
    : {};
  const compilerOptions = { ...options, strict: true, noEmit: true, types: [] };
  const host = ts.createCompilerHost(compilerOptions, true);
  const embeddedLib = resolve(
    dirname(fileURLToPath(import.meta.url)),
    "lib.es2023.full.d.ts",
  );
  if (existsSync(embeddedLib)) host.getDefaultLibFileName = () => embeddedLib;
  const helpers = resolve(root, ".holon-function-types.d.ts");
  const original = host.getSourceFile;
  host.getSourceFile = (path, ...args) =>
    path === helpers
      ? ts.createSourceFile(
          path,
          `declare module "holon-sh/app" {
    export function resource<T>(uri: string, read: (context: unknown) => T): {uri: string; read: (context: unknown) => T};
    export function tool<I,O>(options: unknown, run: (input:I, context:unknown)=>O): {run:(input:I,context:unknown)=>O};
    export function createApp(info: unknown, options: unknown): unknown;
  }`,
          ts.ScriptTarget.Latest,
          true,
        )
      : original(path, ...args);
  const program = ts.createProgram([filename, helpers], compilerOptions, host);
  const checker = program.getTypeChecker();
  const file = program.getSourceFile(filename);
  const fail = (node, message) => {
    const source = node.getSourceFile(),
      pos = source.getLineAndCharacterOfPosition(node.getStart());
    throw new Error(
      `${relative(root, source.fileName)}:${pos.line + 1}:${pos.character + 1}: ${message}`,
    );
  };
  function unwrap(node, seen = new Set()) {
    if (
      ts.isAsExpression(node) ||
      ts.isSatisfiesExpression(node) ||
      ts.isParenthesizedExpression(node)
    )
      return unwrap(node.expression, seen);
    if (!ts.isIdentifier(node)) return node;
    let symbol = checker.getSymbolAtLocation(node);
    if (
      symbol?.valueDeclaration &&
      ts.isShorthandPropertyAssignment(symbol.valueDeclaration)
    )
      symbol = checker.getShorthandAssignmentValueSymbol(
        symbol.valueDeclaration,
      );
    if (symbol?.flags & ts.SymbolFlags.Alias)
      symbol = checker.getAliasedSymbol(symbol);
    const declaration = symbol?.valueDeclaration;
    if (
      declaration &&
      ts.isVariableDeclaration(declaration) &&
      declaration.initializer
    ) {
      if (seen.has(declaration)) fail(node, "Cyclic declaration");
      if (!(declaration.parent.flags & ts.NodeFlags.Const))
        fail(node, "Contract declarations must be const");
      return unwrap(declaration.initializer, new Set(seen).add(declaration));
    }
    return node;
  }
  function object(node) {
    node = unwrap(node);
    if (!ts.isObjectLiteralExpression(node))
      fail(node, "Expected a static object literal");
    const result = {};
    for (const property of node.properties) {
      if (ts.isSpreadAssignment(property))
        Object.assign(result, object(property.expression));
      else if (
        ts.isPropertyAssignment(property) &&
        !ts.isComputedPropertyName(property.name)
      )
        result[property.name.text] = property.initializer;
      else if (ts.isShorthandPropertyAssignment(property))
        result[property.name.text] = property.name;
      else fail(property, "Computed contract properties are unsupported");
    }
    return result;
  }
  function literal(node) {
    node = unwrap(node);
    if (ts.isStringLiteralLike(node)) return node.text;
    if (ts.isNumericLiteral(node)) return Number(node.text);
    if (ts.isPropertyAccessExpression(node))
      return literal(object(node.expression)[node.name.text]);
    if (ts.isTemplateExpression(node))
      return (
        node.head.text +
        node.templateSpans
          .map((span) => String(literal(span.expression)) + span.literal.text)
          .join("")
      );
    if (node.kind === ts.SyntaxKind.TrueKeyword) return true;
    if (node.kind === ts.SyntaxKind.FalseKeyword) return false;
    if (node.kind === ts.SyntaxKind.NullKeyword) return null;
    if (ts.isArrayLiteralExpression(node)) return node.elements.map(literal);
    if (ts.isObjectLiteralExpression(node))
      return Object.fromEntries(
        Object.entries(object(node)).map(([key, value]) => [
          key,
          literal(value),
        ]),
      );
    fail(node, "Metadata must be static JSON");
  }
  function helper(node, name) {
    node = unwrap(node);
    if (!ts.isCallExpression(node)) fail(node, `Expected ${name}(...)`);
    const imported = checker.getSymbolAtLocation(node.expression)
      ?.declarations?.[0];
    if (
      !imported ||
      !ts.isImportSpecifier(imported) ||
      (imported.propertyName ?? imported.name).text !== name ||
      imported.parent.parent.parent.moduleSpecifier.text !== "holon-sh/app"
    )
      fail(node, `Expected ${name} imported from holon-sh/app`);
    return node.arguments;
  }
  function schema(type, at, seen = new Set()) {
    const f = ts.TypeFlags;
    if (type.flags & f.StringLiteral)
      return { type: "string", const: type.value };
    if (type.flags & f.NumberLiteral)
      return { type: "number", const: type.value };
    if (type.flags & f.String) return { type: "string" };
    if (type.flags & f.Number) return { type: "number" };
    if (type.flags & f.BooleanLiteral)
      return { type: "boolean", const: checker.typeToString(type) === "true" };
    if (type.flags & f.Boolean) return { type: "boolean" };
    if (type.flags & f.Null) return { type: "null" };
    if (type.isUnion()) {
      const members = type.types.filter((t) => !(t.flags & f.Undefined));
      if (members.every((t) => t.flags & f.BooleanLiteral))
        return { type: "boolean" };
      return members.length === 1
        ? schema(members[0], at, seen)
        : { anyOf: members.map((t) => schema(t, at, seen)) };
    }
    if (checker.isArrayType(type))
      return {
        type: "array",
        items: schema(checker.getTypeArguments(type)[0], at, seen),
      };
    if (
      !(type.flags & f.Object) ||
      type.getCallSignatures().length ||
      checker.isTupleType(type) ||
      checker.getIndexInfosOfType(type).length ||
      seen.has(type)
    )
      fail(
        at,
        `Unsupported JSON type ${checker.typeToString(type)}; use native MCP schema registration for this type`,
      );
    const next = new Set(seen).add(type),
      properties = {},
      required = [];
    for (const prop of type.getProperties()) {
      const member = checker.getTypeOfSymbolAtLocation(
        prop,
        prop.valueDeclaration ?? at,
      );
      const title = ts.displayPartsToString(
        prop.getDocumentationComment(checker),
      );
      properties[prop.name] = {
        ...schema(member, at, next),
        ...(title ? { title } : {}),
      };
      if (!(prop.flags & ts.SymbolFlags.Optional)) required.push(prop.name);
    }
    return {
      type: "object",
      properties,
      required,
      additionalProperties: false,
    };
  }
  const exported = file.statements.find(ts.isExportAssignment);
  if (!exported) fail(file, "Expected export default createApp(...)");
  const [info, definition] = helper(exported.expression, "createApp");
  const registry = object(definition);
  if (descriptor) {
    const publishing = file.statements
      .filter(ts.isVariableStatement)
      .flatMap((s) => [...s.declarationList.declarations])
      .find((d) => d.name.getText() === "publishing");
    const shared = literal(info),
      declared = publishing ? literal(publishing.initializer) : {};
    for (const key of [
      "name",
      "version",
      "description",
      "title",
      "websiteUrl",
      "icons",
    ])
      if (key in declared)
        fail(publishing, `Declare ${key} in createApp, not publishing`);
    return { descriptor: { ...declared, ...shared } };
  }
  if (registry.configure)
    fail(
      registry.configure,
      "Native configure callbacks cannot change a statically declared catalog; use native MCP registration for this server",
    );
  const resourceEntries = registry.resources
    ? Object.entries(object(registry.resources))
    : [];
  const byDefinition = new Map();
  const resources = resourceEntries.map(([name, node]) => {
    const [uri, read] = helper(node, "resource");
    const signature = checker.getTypeAtLocation(read).getCallSignatures()[0];
    if (!signature) fail(read, "Expected a typed resource function");
    const item = {
      uri: literal(uri),
      outputSchema: schema(
        checker.getAwaitedType(signature.getReturnType()),
        read,
      ),
    };
    byDefinition.set(unwrap(node), item.uri);
    return item;
  });
  const tools = Object.entries(object(registry.tools)).map(([name, node]) => {
    const [options, run] = helper(node, "tool"),
      metadata = object(options);
    const signature = checker.getTypeAtLocation(run).getCallSignatures()[0];
    if (!signature) fail(run, "Expected a typed tool function");
    const first = signature.parameters[0];
    const inputSchema = first
      ? schema(
          checker.getTypeOfSymbolAtLocation(
            first,
            first.valueDeclaration ?? run,
          ),
          run,
        )
      : { type: "object", properties: {}, additionalProperties: false };
    const outputSchema = schema(
      checker.getAwaitedType(signature.getReturnType()),
      run,
    );
    if (inputSchema.type !== "object" || outputSchema.type !== "object")
      fail(
        run,
        "Tool arguments and results must be objects; wrap scalar results in an object or use native MCP registration",
      );
    const item = { name, inputSchema, outputSchema };
    for (const key of [
      "title",
      "description",
      "annotations",
      "icons",
      "execution",
      "_meta",
    ])
      if (metadata[key]) item[key] = literal(metadata[key]);
    let symbol = checker.getSymbolAtLocation(node);
    if (
      symbol?.valueDeclaration &&
      ts.isShorthandPropertyAssignment(symbol.valueDeclaration)
    )
      symbol = checker.getShorthandAssignmentValueSymbol(
        symbol.valueDeclaration,
      );
    if (symbol?.flags & ts.SymbolFlags.Alias)
      symbol = checker.getAliasedSymbol(symbol);
    if (!item.description && symbol)
      item.description = ts.displayPartsToString(
        symbol.getDocumentationComment(checker),
      );
    let changes = [];
    if (metadata.changes) {
      const array = unwrap(metadata.changes);
      if (!ts.isArrayLiteralExpression(array))
        fail(array, "changes must be a static resource array");
      changes = array.elements.map((ref) => {
        const uri = byDefinition.get(unwrap(ref));
        if (!uri) fail(ref, "Changed resources must be registered in this app");
        return uri;
      });
    }
    item._meta = { ...item._meta, "sh.holon/changes": changes };
    return item;
  });
  const sources = {};
  for (const source of program.getSourceFiles()) {
    if (source.fileName === helpers) continue;
    const path = realpathSync(source.fileName);
    if (
      path.startsWith(realpathSync(root) + sep) &&
      !path.includes(`${sep}node_modules${sep}`)
    )
      sources[relative(root, path)] = createHash("sha256")
        .update(readFileSync(path))
        .digest("hex");
  }
  if (configPath)
    sources[relative(root, configPath)] = createHash("sha256")
      .update(readFileSync(configPath))
      .digest("hex");
  return {
    tools: tools.sort((a, b) => a.name.localeCompare(b.name)),
    resources: resources.sort((a, b) => a.uri.localeCompare(b.uri)),
    sources,
  };
}
