import ts from "typescript";
import { z } from "zod";
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { McpServer, createMcpHandler, fromJsonSchema } from "@modelcontextprotocol/server";
import { readFileSync, realpathSync } from "node:fs";
import { resolve, relative, extname, sep } from "node:path";
import { validator } from "./validate.mjs";
import { createHash } from "node:crypto";

class Builtin {
  constructor(module, name) { this.module = module; this.name = name; }
}
class FunctionValue {
  constructor(node, scope) { this.node = node; this.scope = scope; }
}
class Module {
  constructor(symbol) { this.symbol = symbol; }
}
class Schema {
  constructor(value) { this.value = value; }
}
const resourceSchemaKey = "sh.holon/outputSchema";
const identity = { name: "holon-contract", version: "1" };
class Server extends McpServer {
  resources = [];

  async catalog() {
    const server = this.server.getCapabilities().tools ? this : new McpServer(identity, { capabilities: { tools: {} } });
    // createMcpHandler serves 2026-07-28. Supplying that revision skips discovery, so
    // this fresh server handles exactly one request and needs no registration replay.
    const handler = createMcpHandler(() => server, { legacy: "reject" });
    const client = new Client(identity);
    try {
      await client.connect(new StreamableHTTPClientTransport(new URL("http://holon-contract/mcp"), {
        fetch: (url, init) => handler.fetch(new Request(url, init)),
      }), { prior: { kind: "modern", discover: { supportedVersions: ["2026-07-28"], capabilities: server.server.getCapabilities() } } });
      // Request the complete fixed catalog without listTools' client-specific filtering.
      const { tools } = await client.request({ method: "tools/list" });
      return tools;
    } finally {
      await Promise.all([client.close(), handler.close()]);
    }
  }
}
class Lazy {
  constructor(node, scope, mutable = false) { this.node = node; this.scope = scope; this.mutable = mutable; }
}

const sdk = "@modelcontextprotocol/server";
const appRegistrations = new Map([["registerAppTool", "registerTool"], ["registerAppResource", "registerResource"]]);
const zodModules = new Set(["zod", "zod/v4"]);
const constructors = new Set([
  "string", "number", "boolean", "null", "unknown", "never", "any",
  "object", "strictObject", "looseObject", "array", "tuple", "record",
  "enum", "literal", "union", "discriminatedUnion", "intersection",
  "optional", "nullable",
]);
const modifiers = new Set([
  "optional", "nullable", "nullish", "default", "array", "or", "and",
  "min", "max", "length", "int", "positive", "nonnegative", "negative",
  "nonpositive", "multipleOf", "finite", "email", "url", "uuid", "regex",
  "startsWith", "endsWith", "includes", "describe", "strict", "passthrough",
  "strip", "extend", "pick", "omit", "partial", "required",
]);

export async function extract(root, entry) {
  const analyzer = new Analyzer(root);
  try {
    if (!entry) throw new Error("Cannot identify a static MCP handler entrypoint; use the existing server.ts/server.js convention or a direct package main/start entry.");
    const file = analyzer.load(resolve(analyzer.root, entry));
    for (const [file, scope] of analyzer.scopes) analyzer.statements(file.statements, scope);
    const symbol = analyzer.checker.getSymbolAtLocation(file);
    const exported = symbol && analyzer.checker.getExportsOfModule(symbol).find(s => s.name === "default");
    if (!exported) analyzer.fail(file, "Expected the existing default MCP handler export.");
    const result = analyzer.value(analyzer.reference(exported, file));
    if (!(result instanceof Server)) analyzer.fail(file, "Cannot establish the default MCP handler's tool catalog.");
    for (const [file, scope] of analyzer.scopes) analyzer.auditStatements(file.statements, scope);
    for (const block of analyzer.blocks) analyzer.auditStatements(block.statements, block.scope);
    let tools;
    try { tools = await result.catalog(); } catch (error) { analyzer.fail(file, error.message); }
    for (const tool of tools) {
      try {
        validator(tool.inputSchema);
        if (tool.outputSchema) validator(tool.outputSchema);
      } catch (error) { analyzer.fail(file, `Unsupported JSON Schema in ${tool.name}: ${error.message}`); }
    }
    const resources = result.resources.sort((a, b) => a.uri.localeCompare(b.uri));
    for (const resource of resources) {
      try { validator(resource.outputSchema); } catch (error) { analyzer.fail(file, `Unsupported JSON Schema in ${resource.uri}: ${error.message}`); }
    }
    return { tools: tools.sort((a, b) => a.name.localeCompare(b.name)), resources, sources: analyzer.sources };
  } catch (error) {
    return { diagnostic: error.message };
  }
}

class Analyzer {
  constructor(root) {
    this.root = realpathSync(root);
    this.scopes = new Map();
    this.sources = {};
    this.evaluating = new Set();
    this.calls = new Set();
    this.analyzedFunctions = new Set();
    this.blocks = [];
    this.steps = 0;
  }
  fail(node, message) {
    const file = node.getSourceFile();
    const { line, character } = file.getLineAndCharacterOfPosition(node.getStart(file));
    throw new Error(`${relative(this.root, file.fileName)}:${line + 1}:${character + 1}: ${message}`);
  }
  load(path) {
    const options = { target: ts.ScriptTarget.Latest, module: ts.ModuleKind.ESNext, moduleResolution: ts.ModuleResolutionKind.Bundler, moduleDetection: ts.ModuleDetectionKind.Force, allowJs: true, resolveJsonModule: true, noLib: true, types: [] };
    const host = ts.createCompilerHost(options, true);
    // Packages are symbolic builtins. Only local source participates in extraction.
    host.resolveModuleNameLiterals = (names, containingFile) => names.map(node => {
      if (!node.text.startsWith(".") || !(ts.isImportDeclaration(node.parent) || ts.isExportDeclaration(node.parent))) return { resolvedModule: undefined };
      const result = ts.resolveModuleName(node.text, containingFile, options, host);
      if (!result.resolvedModule || result.resolvedModule.isExternalLibraryImport) this.fail(node, `Cannot resolve contract import ${node.text}.`);
      result.resolvedModule.resolvedFileName = realpathSync(result.resolvedModule.resolvedFileName);
      return result;
    });
    host.resolveTypeReferenceDirectiveReferences = names => names.map(() => ({ resolvedTypeReferenceDirective: undefined }));
    host.getSourceFile = (path, languageVersion) => {
      const real = realpathSync(path);
      if (!real.startsWith(this.root + sep)) throw new Error("Contract imports must stay inside the application source.");
      const bytes = readFileSync(real);
      this.sources[relative(this.root, real)] = createHash("sha256").update(bytes).digest("hex");
      return ts.createSourceFile(real, bytes.toString("utf8"), languageVersion, true);
    };
    const program = ts.createProgram([realpathSync(path)], options, host);
    this.checker = program.getTypeChecker();
    for (const file of program.getSourceFiles()) {
      if (file.parseDiagnostics.length) this.fail(file, ts.flattenDiagnosticMessageText(file.parseDiagnostics[0].messageText, "\n"));
      const scope = new Map();
      this.scopes.set(file, scope);
      for (const statement of file.statements) this.bind(statement, scope);
    }
    return program.getSourceFile(realpathSync(path));
  }
  reference(symbol, node) {
    const visited = new Set();
    while (symbol?.flags & ts.SymbolFlags.Alias) {
      if (visited.has(symbol)) this.fail(node, "Cyclic contract import.");
      visited.add(symbol);
      const declaration = symbol.declarations[0];
      const imported = ts.findAncestor(declaration, ts.isImportDeclaration);
      if (imported && !imported.moduleSpecifier.text.startsWith(".")) {
        const name = ts.isImportSpecifier(declaration) ? (declaration.propertyName ?? declaration.name).text : ts.isNamespaceImport(declaration) ? "*" : "default";
        return new Builtin(imported.moduleSpecifier.text, name);
      }
      symbol = this.checker.getImmediateAliasedSymbol(symbol);
    }
    const declaration = symbol?.valueDeclaration ?? symbol?.declarations?.[0];
    if (!declaration || ts.findAncestor(declaration.parent, ts.isFunctionLike)) this.fail(node, `Cannot statically resolve ${node.getText()}.`);
    const file = declaration.getSourceFile();
    const scope = this.scopes.get(file);
    if (scope.has(symbol)) return scope.get(symbol);
    if (ts.isSourceFile(declaration)) return extname(file.fileName) === ".json" ? JSON.parse(file.text) : new Module(symbol);
    if (ts.isExportAssignment(declaration)) {
      const value = new Lazy(declaration.expression, scope);
      scope.set(symbol, value);
      return value;
    }
    if (ts.isFunctionDeclaration(declaration)) return new FunctionValue(declaration, scope);
    this.fail(node, `Cannot statically resolve ${node.getText()}.`);
  }
  lookup(node, scope) {
    const symbol = ts.isShorthandPropertyAssignment(node.parent) ? this.checker.getShorthandAssignmentValueSymbol(node.parent) : this.checker.getSymbolAtLocation(node);
    if (!symbol && node.text === "Object") return new Builtin(null, "Object");
    return scope.has(symbol) ? scope.get(symbol) : this.reference(symbol, node);
  }
  bind(statement, scope) {
    if (ts.isVariableStatement(statement)) for (const d of statement.declarationList.declarations) {
      const symbol = this.checker.getSymbolAtLocation(d.name);
      if (ts.isIdentifier(d.name) && d.initializer && scope.get(symbol)?.node !== d.initializer) scope.set(symbol, new Lazy(d.initializer, scope, !(statement.declarationList.flags & ts.NodeFlags.Const)));
    }
    if (ts.isFunctionDeclaration(statement) && statement.name) scope.set(this.checker.getSymbolAtLocation(statement.name), new FunctionValue(statement, scope));
  }
  value(value) {
    if (value instanceof Lazy) {
      if (value.mutable) this.fail(value.node, "Contract data must use immutable constant bindings.");
      if (this.evaluating.has(value)) this.fail(value.node, "Cyclic contract expression.");
      this.evaluating.add(value);
      try {
        if (!("result" in value)) value.result = this.expression(value.node, value.scope);
        return value.result;
      } finally { this.evaluating.delete(value); }
    }
    return value;
  }
  fields(node, scope) {
    const object = this.expression(node, scope);
    if (object == null || ![null, Object.prototype, Array.prototype, RegExp.prototype].includes(Object.getPrototypeOf(object))) this.fail(node, "Expected a statically declared object.");
    return object;
  }
  expression(node, scope) {
    if (++this.steps > 50000) this.fail(node, "Contract analysis exceeded its complexity limit.");
    if (ts.isParenthesizedExpression(node) || ts.isAssertionExpression(node) || ts.isSatisfiesExpression(node) || ts.isNonNullExpression(node) || ts.isAwaitExpression(node)) return this.expression(node.expression, scope);
    if (ts.isStringLiteralLike(node) || ts.isNumericLiteral(node)) return ts.isNumericLiteral(node) ? Number(node.text) : node.text;
    if (node.kind === ts.SyntaxKind.TrueKeyword) return true;
    if (node.kind === ts.SyntaxKind.FalseKeyword) return false;
    if (node.kind === ts.SyntaxKind.NullKeyword) return null;
    if (ts.isIdentifier(node)) {
      if (node.text === "undefined") return undefined;
      return this.value(this.lookup(node, scope));
    }
    if (ts.isArrowFunction(node) || ts.isFunctionExpression(node)) return new FunctionValue(node, scope);
    if (ts.isObjectLiteralExpression(node)) {
      const object = Object.create(null);
      for (const p of node.properties) {
        if (ts.isSpreadAssignment(p)) Object.assign(object, this.fields(p.expression, scope));
        else if (ts.isShorthandPropertyAssignment(p)) object[p.name.text] = new Lazy(p.name, scope);
        else if (ts.isPropertyAssignment(p)) {
          const key = ts.isComputedPropertyName(p.name) ? this.expression(p.name.expression, scope) : p.name.text;
          if (typeof key !== "string" && typeof key !== "number") this.fail(p, "Computed schema keys must be constant strings.");
          object[key] = new Lazy(p.initializer, scope);
        } else this.fail(p, "Schema getters and methods cannot be evaluated statically.");
      }
      return object;
    }
    if (ts.isArrayLiteralExpression(node)) return node.elements.flatMap(e => ts.isSpreadElement(e) ? this.expression(e.expression, scope) : [new Lazy(e, scope)]);
    if (ts.isPrefixUnaryExpression(node)) {
      const value = this.expression(node.operand, scope);
      if (node.operator === ts.SyntaxKind.MinusToken && typeof value === "number") return -value;
      if (node.operator === ts.SyntaxKind.PlusToken && typeof value === "number") return value;
      if (node.operator === ts.SyntaxKind.ExclamationToken && typeof value === "boolean") return !value;
    }
    if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
      const object = this.expression(node.expression, scope);
      const key = ts.isPropertyAccessExpression(node) ? node.name.text : this.expression(node.argumentExpression, scope);
      if (object instanceof Builtin) return new Builtin(object.module, ["*", "z", "default"].includes(object.name) ? key : `${object.name}.${key}`);
      if (object instanceof Server && key === "server") return object;
      if (object instanceof Module) return this.value(this.reference(this.checker.getExportsOfModule(object.symbol).find(s => s.name === key), node));
      if (object && !(object instanceof Server) && Object.hasOwn(object, key)) return this.value(object[key]);
      this.fail(node, `Cannot statically resolve property ${key}.`);
    }
    if (ts.isNewExpression(node)) {
      const target = this.expression(node.expression, scope);
      if (target instanceof Builtin && target.module === sdk && target.name === "McpServer") return new Server(identity);
      this.fail(node, "Only an official McpServer constructor is supported in a contract expression.");
    }
    if (node.kind === ts.SyntaxKind.RegularExpressionLiteral) {
      const text = node.getText();
      const end = text.lastIndexOf("/");
      return new RegExp(text.slice(1, end), text.slice(end + 1));
    }
    if (ts.isCallExpression(node)) {
      if (ts.isPropertyAccessExpression(node.expression)) {
        const receiver = this.expression(node.expression.expression, scope);
        const method = node.expression.name.text;
        if (receiver instanceof Schema) {
          // Zod refinements constrain runtime validation but add no JSON Schema.
          // Keep the underlying wire schema without evaluating application callbacks.
          if (["refine", "superRefine"].includes(method)) {
            for (const argument of node.arguments) {
              if (this.containsRegistration(argument, scope)) this.fail(argument, "Schema callbacks must not change the tool catalog.");
              this.auditCallback(argument, scope);
            }
            return receiver;
          }
          if (!modifiers.has(method)) this.fail(node, `Schema method ${method} has no supported static wire contract.`);
          return new Schema(receiver.value[method](...node.arguments.map(a => this.materialize(new Lazy(a, scope)))));
        }
      }
      const target = this.expression(node.expression, scope);
      if (target instanceof Builtin && target.module === null && target.name === "Object.keys") {
        if (node.arguments.length !== 1) this.fail(node, "Object.keys requires one static object argument.");
        return Object.keys(this.fields(node.arguments[0], scope));
      }
      if (target instanceof Builtin && target.module === sdk && target.name === "createMcpHandler") {
        const factory = this.expression(node.arguments[0], scope);
        return this.functionBody(factory, [], node);
      }
      if (target instanceof Builtin && target.module === sdk && target.name === "fromJsonSchema") {
        const schema = this.materialize(new Lazy(node.arguments[0], scope));
        try { return new Schema(fromJsonSchema(schema)); } catch (error) { this.fail(node, `Unsupported JSON Schema: ${error.message}`); }
      }
      if (target instanceof Builtin && zodModules.has(target.module)) {
        if (target.name === "toJSONSchema") {
          const schema = this.expression(node.arguments[0], scope);
          if (!(schema instanceof Schema)) this.fail(node, "Expected a statically supported Zod schema.");
          const options = node.arguments[1] ? this.materialize(new Lazy(node.arguments[1], scope)) : {};
          if (Object.keys(options).some(k => !["io", "target"].includes(k))) this.fail(node, "Unsupported JSON Schema conversion options.");
          return z.toJSONSchema(schema.value, options);
        }
        if (!constructors.has(target.name)) this.fail(node, `Zod ${target.name} has no supported static wire contract.`);
        return new Schema(z[target.name](...node.arguments.map(a => this.materialize(new Lazy(a, scope)))));
      }
      this.fail(node, "Application functions are not executed to infer contracts.");
    }
    this.fail(node, "Expression is not a supported static contract.");
  }
  materialize(value) {
    value = this.value(value);
    if (value instanceof Schema) return value.value;
    if (value instanceof FunctionValue || value instanceof Builtin || value instanceof Server || value instanceof Module) throw new Error("Application callbacks and runtime values are not valid static schema data.");
    if (value instanceof RegExp) return value;
    if (Array.isArray(value)) return value.map(v => this.materialize(v));
    if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, this.materialize(v)]));
    return value;
  }
  functionBody(fn, args, node, fixedCatalog = false) {
    if (!(fn instanceof FunctionValue)) this.fail(node, "Expected a statically declared server factory or registration helper.");
    if (!fixedCatalog && fn.node.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.AsyncKeyword) && !ts.isAwaitExpression(node.parent)) {
      let target;
      try { if (ts.isCallExpression(node)) target = this.expression(node.expression, fn.scope); } catch {}
      if (!(target instanceof Builtin && target.module === sdk && target.name === "createMcpHandler")) this.fail(node, "Asynchronous registration helpers must be awaited to establish a complete catalog.");
    }
    if (this.calls.has(fn.node)) this.fail(node, "Recursive registration helpers cannot establish a static catalog.");
    this.calls.add(fn.node);
    this.analyzedFunctions.add(fn.node);
    try {
      const scope = new Map(fn.scope);
      fn.node.parameters.forEach((p, i) => { if (ts.isIdentifier(p.name)) scope.set(this.checker.getSymbolAtLocation(p.name), args[i]); });
      if (!fn.node.body) this.fail(node, "Missing factory body.");
      if (!ts.isBlock(fn.node.body)) {
        const body = fn.node.body;
        if (ts.isCallExpression(body)) {
          const target = this.expression(body.expression, scope);
          if (target instanceof FunctionValue) return this.functionBody(target, body.arguments.map(a => new Lazy(a, scope)), body);
        }
        return this.expression(body, scope);
      }
      this.blocks.push({ statements: fn.node.body.statements, scope });
      return this.statements(fn.node.body.statements, scope, fixedCatalog);
    } finally { this.calls.delete(fn.node); }
  }
  containsRegistration(node, scope, visited = new Set()) {
    const visit = n => {
      if (ts.isTypeNode(n)) return;
      // Untyped resources do not contribute to the generated wire contract. Their
      // callbacks still cannot mutate the catalog, including inside resource loops.
      if (ts.isCallExpression(n) && ts.isPropertyAccessExpression(n.expression) && n.expression.name.text === "registerResource") {
        try {
          const server = this.expression(n.expression.expression, scope);
          const config = this.fields(n.arguments[2], scope);
          const meta = this.value(config._meta);
          if (server instanceof Server && (!meta || !Object.hasOwn(meta, resourceSchemaKey))) {
            return n.arguments.some(arg => this.containsRegistration(arg, scope, visited));
          }
        } catch {}
      }
      if (visited.has(n)) return;
      visited.add(n);
      if (ts.isPropertyAccessExpression(n) && ["registerTool", "setRequestHandler", "update", "remove", "disable", "enable"].includes(n.name.text)) return true;
      if (ts.isIdentifier(n)) {
        let value;
        try { value = this.value(this.lookup(n, scope)); } catch {}
        if (value instanceof Server) return true;
        if (value instanceof FunctionValue && value.node.body && this.containsRegistration(value.node.body, value.scope, visited)) return true;
      }
      return ts.forEachChild(n, visit);
    };
    return !!visit(node);
  }
  statements(statements, scope, fixedCatalog = false) {
    for (const statement of statements) {
      this.bind(statement, scope);
      if (ts.isThrowStatement(statement)) this.fail(statement, "A throwing factory cannot establish a tool catalog.");
      if (ts.isReturnStatement(statement)) return statement.expression ? this.expression(statement.expression, scope) : undefined;
      if (ts.isIfStatement(statement) || ts.isIterationStatement(statement, false) || ts.isSwitchStatement(statement) || ts.isTryStatement(statement)) {
        if (fixedCatalog || this.containsRegistration(statement, scope)) this.fail(statement, "Conditional or repeated registration cannot establish a complete static catalog.");
        continue;
      }
      if (ts.isVariableStatement(statement)) {
        for (const declaration of statement.declarationList.declarations) {
          if (!ts.isIdentifier(declaration.name) && declaration.initializer && this.containsRegistration(declaration.initializer, scope)) this.fail(declaration, "Destructured server methods cannot establish a static catalog.");
          if (declaration.initializer && ts.isFunctionLike(declaration.initializer)) continue;
          if (declaration.initializer && !ts.isIdentifier(declaration.initializer) && this.containsRegistration(declaration.initializer, scope)) {
            let target;
            if (ts.isCallExpression(declaration.initializer)) {
              try { target = this.expression(declaration.initializer.expression, scope); } catch {}
            }
            if (target instanceof Builtin && target.module === sdk && target.name === "createMcpHandler") this.value(this.lookup(declaration.name, scope));
            else this.statementExpression(declaration.initializer, scope);
          }
        }
        continue;
      }
      if (!ts.isExpressionStatement(statement)) continue;
      const expression = statement.expression;
      this.statementExpression(expression, scope);
    }
    return undefined;
  }
  containsContractData(node, scope) {
    const visit = n => {
      if (ts.isFunctionLike(n) || ts.isTypeNode(n)) return;
      if (ts.isIdentifier(n)) {
        let value;
        try { value = this.lookup(n, scope); } catch {}
        if (value instanceof Lazy) value = value.result;
        if (value && typeof value === "object" && !(value instanceof Builtin) && !(value instanceof FunctionValue) && !(value instanceof Server)) return true;
      }
      return ts.forEachChild(n, visit);
    };
    return !!visit(node);
  }
  auditStatements(statements, scope) {
    for (const statement of statements) {
      if (ts.isIfStatement(statement) || ts.isIterationStatement(statement, false) || ts.isSwitchStatement(statement) || ts.isTryStatement(statement)) {
        if (this.containsContractData(statement, scope)) this.fail(statement, "Conditional schema or catalog mutation is unsupported.");
      }
      if (ts.isExpressionStatement(statement) && !this.containsRegistration(statement, scope)) this.statementExpression(statement.expression, scope);
      if (ts.isExpressionStatement(statement) && ts.isCallExpression(statement.expression)) {
        for (const argument of statement.expression.arguments) this.auditCallback(argument, scope);
      }
    }
  }
  auditCallback(node, scope) {
    let fn;
    try { fn = this.expression(node, scope); } catch { return; }
    if (!(fn instanceof FunctionValue) || this.analyzedFunctions.has(fn.node)) return;
    const visit = node => {
      if (ts.isBinaryExpression(node) && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment && this.containsContractData(node.left, fn.scope)) this.fail(node, "Deferred schema or catalog mutation is unsupported.");
      if (ts.isDeleteExpression(node) && this.containsContractData(node.expression, fn.scope)) this.fail(node, "Deferred schema or catalog mutation is unsupported.");
      if (ts.isCallExpression(node) && this.containsContractData(node, fn.scope)) {
        const method = node.expression;
        let receiver;
        try { if (ts.isPropertyAccessExpression(method)) receiver = this.expression(method.expression, fn.scope); } catch {}
        const schemaRead = receiver instanceof Schema && (modifiers.has(method.name.text) || ["parse", "safeParse", "parseAsync", "safeParseAsync"].includes(method.name.text));
        const conversion = receiver instanceof Builtin && zodModules.has(receiver.module) && method.name?.text === "toJSONSchema";
        if (!schemaRead && !conversion) this.fail(node, "Schema or catalog data escapes through a deferred callback.");
      }
      ts.forEachChild(node, visit);
    };
    if (fn.node.body) visit(fn.node.body);
  }
  resource(server, args, expression, scope) {
    let config;
    try { config = this.fields(args[2], scope); } catch { return; }
    const meta = config._meta === undefined ? undefined : this.value(config._meta);
    if (!meta || typeof meta !== "object" || !Object.hasOwn(meta, resourceSchemaKey)) return;
    const uri = this.expression(args[1], scope);
    if (typeof uri !== "string") this.fail(expression, "Typed resource registration must have a constant URI.");
    if (this.value(config.mimeType) !== "application/json") this.fail(expression, `Typed resource ${uri} must declare the application/json mimeType.`);
    server.resources.push({ uri, outputSchema: this.materialize(meta[resourceSchemaKey]) });
  }
  register(server, methodName, args, expression, scope) {
    if (methodName === "registerTool") {
      const name = this.expression(args[0], scope);
      if (typeof name !== "string") this.fail(expression, "Tool registration must have a constant string name.");
      const config = this.fields(args[1], scope);
      if (this.containsRegistration(args[2], scope)) this.fail(args[2], "Tool handlers must not change the tool catalog.");
      // Only analyzed schemas cross into the SDK. Application callbacks are never installed.
      try {
        server.registerTool(name, { inputSchema: this.materialize(config.inputSchema), outputSchema: this.materialize(config.outputSchema) }, () => { throw new Error("Contract extraction cannot call tools."); });
      } catch (error) { this.fail(expression, error.message); }
    } else if (methodName === "setRequestHandler") {
      const name = this.expression(args[0], scope);
      if (name !== "tools/list") {
        if (args.slice(1).some(arg => this.containsRegistration(arg, scope))) this.fail(expression, "Request handlers must not change the tool catalog.");
        return;
      }
      try { server.server.assertCanSetRequestHandler("tools/list"); } catch (error) { this.fail(expression, error.message); }
      const fn = this.expression(args.at(-1), scope);
      const result = this.functionBody(fn, [], expression, true);
      if (result.nextCursor) this.fail(expression, "A paginated tool catalog is not a fixed complete catalog.");
      const tools = this.value(result.tools);
      if (!Array.isArray(tools)) this.fail(expression, "tools/list must return a fixed tools array.");
      const catalog = tools.map(t => {
        const object = this.value(t);
        const tool = { name: this.value(object.name), inputSchema: this.materialize(object.inputSchema) };
        if (object.outputSchema) tool.outputSchema = this.materialize(object.outputSchema);
        return tool;
      });
      server.server.registerCapabilities({ tools: {} });
      server.server.setRequestHandler("tools/list", () => ({ tools: catalog }));
    } else {
      if (!["registerResource", "registerPrompt", "registerResourceTemplate"].includes(methodName)) this.fail(expression, `Server mutation ${methodName} is unsupported during static extraction.`);
      if (args.some(arg => this.containsRegistration(arg, scope))) this.fail(expression, "Resource and prompt handlers must not change the tool catalog.");
      if (methodName === "registerResource") this.resource(server, args, expression, scope);
    }
  }
  statementExpression(expression, scope) {
      if (ts.isAwaitExpression(expression)) expression = expression.expression;
      if (ts.isBinaryExpression(expression) && expression.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && expression.operatorToken.kind <= ts.SyntaxKind.LastAssignment) {
        const target = expression.left;
        let receiver;
        try {
          if (ts.isPropertyAccessExpression(target) || ts.isElementAccessExpression(target)) receiver = this.expression(target.expression, scope);
        } catch {}
        if (receiver instanceof Builtin && receiver.module !== null && receiver.module !== sdk && !zodModules.has(receiver.module) && !this.containsRegistration(expression, scope) && !this.containsContractData(expression, scope)) return;
        this.fail(expression, "Mutation in server registration code cannot establish a static catalog.");
      }
      if (!ts.isCallExpression(expression)) {
        if (this.containsRegistration(expression, scope)) this.fail(expression, "Unsupported registration expression.");
        return;
      }
      const method = expression.expression;
      let helper;
      try { helper = this.expression(method, scope); } catch {}
      if (helper instanceof Builtin && helper.module === "@modelcontextprotocol/ext-apps/server" && appRegistrations.has(helper.name)) {
        const server = this.expression(expression.arguments[0], scope);
        if (!(server instanceof Server)) this.fail(expression, "Expected an official McpServer for the MCP App registration.");
        return this.register(server, appRegistrations.get(helper.name), expression.arguments.slice(1), expression, scope);
      }
      if (ts.isPropertyAccessExpression(method) || ts.isElementAccessExpression(method)) {
        const methodName = ts.isPropertyAccessExpression(method) ? method.name.text : this.expression(method.argumentExpression, scope);
        let receiver;
        try { receiver = this.expression(method.expression, scope); } catch {
          if (this.containsRegistration(expression, scope)) this.fail(expression, "Cannot resolve the receiver of a tool registration.");
          if (this.containsContractData(expression, scope)) this.fail(expression, "Schema data escapes static analysis.");
          return;
        }
        if (!(receiver instanceof Server)) {
          if (receiver && typeof receiver === "object" && !(receiver instanceof Builtin)) this.fail(expression, "Mutation of schema or catalog data is unsupported.");
          if (this.containsRegistration(expression, scope)) this.fail(expression, "Server escape or deferred registration is unsupported.");
          if (this.containsContractData(expression, scope)) this.fail(expression, "Schema data escapes static analysis.");
          return;
        }
        this.register(receiver, methodName, expression.arguments, expression, scope);
      } else {
        let fn;
        try { fn = this.expression(method, scope); } catch {
          if (this.containsRegistration(expression, scope)) this.fail(expression, "Unresolved registration helper.");
          if (this.containsContractData(expression, scope)) this.fail(expression, "Schema data escapes static analysis.");
          return;
        }
        if (fn instanceof FunctionValue) this.functionBody(fn, expression.arguments.map(a => new Lazy(a, scope)), expression);
        else if (this.containsRegistration(expression, scope)) this.fail(expression, "Server escape or deferred registration is unsupported.");
        else if (this.containsContractData(expression, scope)) this.fail(expression, "Schema data escapes static analysis.");
      }
  }
}
