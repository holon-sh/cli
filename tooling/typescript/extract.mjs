import ts from "typescript";
import { z } from "zod";
import { readFileSync, existsSync, realpathSync, readdirSync } from "node:fs";
import { resolve, dirname, relative, extname, sep } from "node:path";
import { validator } from "./validate.mjs";
import { createHash } from "node:crypto";

class Builtin {
  constructor(module, name) { this.module = module; this.name = name; }
}
class FunctionValue {
  constructor(node, scope) { this.node = node; this.scope = scope; }
}
class Schema {
  constructor(value) { this.value = value; }
}
const resourceSchemaKey = "sh.holon/outputSchema";
class Server {
  tools = new Map();
  resources = new Map();
  list = undefined;
}
class Lazy {
  constructor(node, scope, mutable = false) { this.node = node; this.scope = scope; this.mutable = mutable; }
}

const sdk = "@modelcontextprotocol/server";
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

function identifierReference(node) {
  const parent = node.parent;
  return !((ts.isPropertyAccessExpression(parent) || ts.isPropertyAssignment(parent)) && parent.name === node);
}

export function extract(root, entry) {
  const analyzer = new Analyzer(root);
  try {
    if (!entry) throw new Error("Cannot identify a static MCP handler entrypoint; use the existing server.ts/server.js convention or a direct package main/start entry.");
    const module = analyzer.module(resolve(analyzer.root, entry));
    for (const loaded of [...analyzer.modules.values()].reverse()) analyzer.statements(loaded.file.statements, loaded.scope);
    const exported = module.exports.get("default");
    if (!exported) analyzer.fail(module.file, "Expected the existing default MCP handler export.");
    const result = analyzer.value(exported);
    if (!(result instanceof Server)) analyzer.fail(module.file, "Cannot establish the default MCP handler's tool catalog.");
    for (const module of analyzer.modules.values()) analyzer.auditStatements(module.file.statements, module.scope);
    for (const block of analyzer.blocks) analyzer.auditStatements(block.statements, block.scope);
    const tools = result.list ?? [...result.tools.values()];
    const names = new Set();
    for (const tool of tools) {
      if (typeof tool.name !== "string" || !tool.name || names.has(tool.name)) analyzer.fail(module.file, "Tool names must be unique constant strings.");
      names.add(tool.name);
      if (!tool.inputSchema || typeof tool.inputSchema !== "object" || Array.isArray(tool.inputSchema) || tool.inputSchema.type !== "object") analyzer.fail(module.file, `Invalid object input schema for ${tool.name}.`);
      if (Object.hasOwn(tool, "outputSchema") && (!tool.outputSchema || typeof tool.outputSchema !== "object" || Array.isArray(tool.outputSchema))) analyzer.fail(module.file, `Invalid output schema for ${tool.name}.`);
      try {
        validator(tool.inputSchema);
        if (tool.outputSchema) validator(tool.outputSchema);
      } catch (error) { analyzer.fail(module.file, `Unsupported JSON Schema in ${tool.name}: ${error.message}`); }
    }
    const resources = [...result.resources.values()].sort((a, b) => a.uri.localeCompare(b.uri));
    for (const resource of resources) {
      if (!resource.outputSchema || typeof resource.outputSchema !== "object" || Array.isArray(resource.outputSchema)) analyzer.fail(module.file, `Invalid contents schema for ${resource.uri}.`);
      try { validator(resource.outputSchema); } catch (error) { analyzer.fail(module.file, `Unsupported JSON Schema in ${resource.uri}: ${error.message}`); }
    }
    return { tools: tools.sort((a, b) => a.name.localeCompare(b.name)), resources, usesApps: analyzer.usesApps, sources: analyzer.sources };
  } catch (error) {
    return { diagnostic: error.message, usesApps: analyzer.usesApps };
  }
}

// Ignore installed dependencies: only the application's own imports select typed generation.
function usesApps(root) {
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    if (["node_modules", ".git", ".holon", ".holon-types"].includes(entry.name)) continue;
    const path = resolve(root, entry.name);
    if (entry.isDirectory() && usesApps(path)) return true;
    if (entry.isFile() && /\.[cm]?[jt]sx?$/.test(entry.name) && /@holon-sh\/apps(?:[\/"'])/.test(readFileSync(path, "utf8"))) return true;
  }
  return false;
}

class Analyzer {
  constructor(root) {
    this.root = realpathSync(root);
    this.modules = new Map();
    this.sources = {};
    this.evaluating = new Set();
    this.calls = new Set();
    this.analyzedFunctions = new Set();
    this.blocks = [];
    this.usesApps = usesApps(this.root);
    this.steps = 0;
  }
  fail(node, message) {
    const file = node.getSourceFile();
    const { line, character } = file.getLineAndCharacterOfPosition(node.getStart(file));
    throw new Error(`${relative(this.root, file.fileName)}:${line + 1}:${character + 1}: ${message}`);
  }
  module(path) {
    if (this.modules.has(path)) return this.modules.get(path);
    const real = realpathSync(path);
    if (real !== this.root && !real.startsWith(this.root + sep)) throw new Error("Contract imports must stay inside the application source.");
    const text = readFileSync(path, "utf8");
    this.sources[relative(this.root, real)] = createHash("sha256").update(text).digest("hex");
    const file = ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true);
    if (file.parseDiagnostics.length) this.fail(file, ts.flattenDiagnosticMessageText(file.parseDiagnostics[0].messageText, "\n"));
    const scope = new Map();
    const result = { file, scope, exports: new Map() };
    this.modules.set(path, result);
    for (const statement of file.statements) {
      if (ts.isImportDeclaration(statement)) {
        const specifier = statement.moduleSpecifier.text;
        if (/^@holon-sh\/apps(?:\/|$)/.test(specifier)) this.usesApps = true;
        const clause = statement.importClause;
        if (!clause) {
          if (specifier.startsWith(".")) this.module(this.resolveImport(dirname(path), specifier, statement));
          continue;
        }
        const imported = name => {
          if (!specifier.startsWith(".")) return new Builtin(specifier, name);
          const target = this.resolveImport(dirname(path), specifier, statement);
          if (extname(target) === ".json") {
            const text = readFileSync(target, "utf8");
            this.sources[relative(this.root, target)] = createHash("sha256").update(text).digest("hex");
            return JSON.parse(text);
          }
          const other = this.module(target);
          if (name === "*") return other.exports;
          return { imported: other, name, node: statement };
        };
        if (clause.name) scope.set(clause.name.text, imported("default"));
        if (clause.namedBindings && ts.isNamespaceImport(clause.namedBindings)) scope.set(clause.namedBindings.name.text, imported("*"));
        if (clause.namedBindings && ts.isNamedImports(clause.namedBindings)) {
          for (const item of clause.namedBindings.elements) scope.set(item.name.text, imported((item.propertyName ?? item.name).text));
        }
      }
      this.bind(statement, scope);
      const exported = statement.modifiers?.some(m => m.kind === ts.SyntaxKind.ExportKeyword);
      if (exported && ts.isVariableStatement(statement)) {
        for (const d of statement.declarationList.declarations) if (ts.isIdentifier(d.name)) result.exports.set(d.name.text, scope.get(d.name.text));
      }
      if (exported && ts.isFunctionDeclaration(statement) && statement.name) result.exports.set(statement.name.text, scope.get(statement.name.text));
      if (ts.isExportAssignment(statement)) result.exports.set("default", new Lazy(statement.expression, scope));
      if (ts.isExportDeclaration(statement) && statement.exportClause && ts.isNamedExports(statement.exportClause)) {
        for (const item of statement.exportClause.elements) {
          if (statement.moduleSpecifier) {
            const target = this.module(this.resolveImport(dirname(path), statement.moduleSpecifier.text, statement));
            result.exports.set(item.name.text, { imported: target, name: (item.propertyName ?? item.name).text, node: statement });
          } else result.exports.set(item.name.text, new Lazy(item.propertyName ?? item.name, scope));
        }
      }
    }
    return result;
  }
  resolveImport(directory, name, node) {
    const base = resolve(directory, name);
    const stems = /\.[cm]?js$/.test(base) ? [base.replace(/\.[cm]?js$/, ".ts"), base] : [base];
    for (const stem of stems) for (const suffix of ["", ".ts", ".js", ".mts", ".mjs", "/index.ts", "/index.js"]) {
      const candidate = stem + suffix;
      if (existsSync(candidate) && /\.(?:[cm]?[jt]s|json)$/.test(candidate)) {
        const real = realpathSync(candidate);
        if (!real.startsWith(this.root + sep)) this.fail(node, "Contract imports must stay inside the application source.");
        return real;
      }
    }
    this.fail(node, `Cannot resolve contract import ${name}.`);
  }
  bind(statement, scope) {
    if (ts.isVariableStatement(statement)) for (const d of statement.declarationList.declarations) {
      if (ts.isIdentifier(d.name) && d.initializer && scope.get(d.name.text)?.node !== d.initializer) scope.set(d.name.text, new Lazy(d.initializer, scope, !(statement.declarationList.flags & ts.NodeFlags.Const)));
    }
    if (ts.isFunctionDeclaration(statement) && statement.name) scope.set(statement.name.text, new FunctionValue(statement, scope));
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
    if (value?.imported) {
      if (!value.imported.exports.has(value.name)) this.fail(value.node, `Cannot resolve exported constant ${value.name}.`);
      return this.value(value.imported.exports.get(value.name));
    }
    return value;
  }
  fields(node, scope) {
    const object = this.expression(node, scope);
    if (object === null || typeof object !== "object" || object instanceof Schema || object instanceof Builtin) this.fail(node, "Expected a statically declared object.");
    return object;
  }
  expression(node, scope) {
    if (++this.steps > 50000) this.fail(node, "Contract analysis exceeded its complexity limit.");
    if (ts.isParenthesizedExpression(node) || ts.isAsExpression(node) || ts.isSatisfiesExpression(node) || ts.isTypeAssertionExpression(node) || ts.isNonNullExpression(node) || ts.isAwaitExpression(node)) return this.expression(node.expression, scope);
    if (ts.isStringLiteralLike(node) || ts.isNumericLiteral(node)) return ts.isNumericLiteral(node) ? Number(node.text) : node.text;
    if (node.kind === ts.SyntaxKind.TrueKeyword) return true;
    if (node.kind === ts.SyntaxKind.FalseKeyword) return false;
    if (node.kind === ts.SyntaxKind.NullKeyword) return null;
    if (ts.isIdentifier(node)) {
      if (node.text === "undefined") return undefined;
      if (!scope.has(node.text)) this.fail(node, `Cannot statically resolve ${node.text}.`);
      return this.value(scope.get(node.text));
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
      if (object instanceof Map) return this.value(object.get(key));
      if (object && Object.hasOwn(object, key)) return this.value(object[key]);
      this.fail(node, `Cannot statically resolve property ${key}.`);
    }
    if (ts.isNewExpression(node)) {
      const target = this.expression(node.expression, scope);
      if (target instanceof Builtin && target.module === sdk && target.name === "McpServer") return new Server();
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
          if (!modifiers.has(method)) this.fail(node, `Schema method ${method} has no supported static wire contract.`);
          const args = node.arguments.map(a => this.materialize(new Lazy(a, scope)));
          if (args.some(a => a instanceof FunctionValue)) this.fail(node, "Schema callbacks are not executed during contract extraction.");
          return new Schema(receiver.value[method](...args));
        }
      }
      const target = this.expression(node.expression, scope);
      if (target instanceof Builtin && target.module === sdk && target.name === "createMcpHandler") {
        const factory = this.expression(node.arguments[0], scope);
        return this.functionBody(factory, [], node);
      }
      if (target instanceof Builtin && target.module === sdk && target.name === "fromJsonSchema") {
        return { jsonSchema: this.materialize(new Lazy(node.arguments[0], scope)) };
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
    if (value instanceof FunctionValue || value instanceof Builtin || value instanceof Server) throw new Error("Application callbacks and runtime values are not valid static schema data.");
    if (value instanceof RegExp) return value;
    if (Array.isArray(value)) return value.map(v => this.materialize(v));
    if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, this.materialize(v)]));
    return value;
  }
  schema(value, io, node) {
    value = this.value(value);
    if (value === undefined && io === "input") return { type: "object", properties: {} };
    let schema;
    if (value instanceof Schema) schema = z.toJSONSchema(value.value, { io });
    else if (value && Object.hasOwn(value, "jsonSchema")) schema = value.jsonSchema;
    else this.fail(node, "Expected a supported schema object or fromJsonSchema declaration.");
    if (!schema || typeof schema !== "object" || Array.isArray(schema)) this.fail(node, "An MCP tool schema must be a JSON Schema object.");
    if (io === "input") {
      if (schema.type !== undefined && schema.type !== "object") this.fail(node, "MCP tool inputs must describe objects.");
      return {type: "object", ...schema};
    }
    const objectShaped = schema => {
      if (["properties", "patternProperties", "additionalProperties", "required"].some(key => Object.hasOwn(schema, key))) return true;
      for (const key of ["oneOf", "anyOf", "allOf"]) {
        if (Array.isArray(schema[key]) && schema[key].length) return schema[key].every(child => child && typeof child === "object" && (child.type === "object" || objectShaped(child)));
      }
      return false;
    };
    return schema.type === undefined && objectShaped(schema) ? {type:"object", ...schema} : schema;
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
      fn.node.parameters.forEach((p, i) => { if (ts.isIdentifier(p.name)) scope.set(p.name.text, args[i]); });
      if (!fn.node.body) this.fail(node, "Missing factory body.");
      if (!ts.isBlock(fn.node.body)) {
        const body = fn.node.body;
        if (ts.isCallExpression(body)) {
          const target = this.expression(body.expression, scope);
          if (target instanceof FunctionValue) return this.functionBody(target, body.arguments.map(a => new Lazy(a, scope)), body);
        }
        return this.expression(body, scope);
      }
      return this.statements(fn.node.body.statements, scope, fixedCatalog);
    } finally { this.calls.delete(fn.node); }
  }
  containsRegistration(node, scope, visited = new Set()) {
    let found = false;
    const visit = n => {
      if (ts.isTypeNode(n)) return;
      if (visited.has(n)) return;
      visited.add(n);
      if (ts.isPropertyAccessExpression(n) && ["registerTool", "setRequestHandler", "update", "remove", "disable", "enable"].includes(n.name.text)) found = true;
      if (ts.isIdentifier(n) && identifierReference(n) && scope?.has(n.text)) {
        let value;
        try { value = this.value(scope.get(n.text)); } catch {}
        if (value instanceof Server) found = true;
        if (value instanceof FunctionValue && value.node.body && this.containsRegistration(value.node.body, value.scope, visited)) found = true;
      }
      ts.forEachChild(n, visit);
    };
    visit(node);
    return found;
  }
  statements(statements, scope, fixedCatalog = false) {
    this.blocks.push({ statements, scope });
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
            if (target instanceof Builtin && target.module === sdk && target.name === "createMcpHandler") this.value(scope.get(declaration.name.text));
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
    let found = false;
    const visit = n => {
      if (ts.isFunctionLike(n) || ts.isTypeNode(n)) return;
      if (ts.isIdentifier(n) && identifierReference(n)) {
        let value = scope.get(n.text);
        if (value?.imported) value = value.imported.exports.get(value.name);
        if (value instanceof Lazy) value = value.result;
        if (value && typeof value === "object" && !(value instanceof Builtin) && !(value instanceof FunctionValue) && !(value instanceof Server)) found = true;
      }
      ts.forEachChild(n, visit);
    };
    visit(node);
    return found;
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
    const local = new Map(fn.scope);
    for (const parameter of fn.node.parameters) if (ts.isIdentifier(parameter.name)) local.delete(parameter.name.text);
    const visit = node => {
      if (ts.isBinaryExpression(node) && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment && this.containsContractData(node.left, local)) this.fail(node, "Deferred schema or catalog mutation is unsupported.");
      if (ts.isDeleteExpression(node) && this.containsContractData(node.expression, local)) this.fail(node, "Deferred schema or catalog mutation is unsupported.");
      if (ts.isCallExpression(node) && this.containsContractData(node, local)) {
        const method = node.expression;
        let receiver;
        try { if (ts.isPropertyAccessExpression(method)) receiver = this.expression(method.expression, local); } catch {}
        const schemaRead = receiver instanceof Schema && (modifiers.has(method.name.text) || ["parse", "safeParse", "parseAsync", "safeParseAsync"].includes(method.name.text));
        const conversion = receiver instanceof Builtin && zodModules.has(receiver.module) && method.name?.text === "toJSONSchema";
        if (!schemaRead && !conversion) this.fail(node, "Schema or catalog data escapes through a deferred callback.");
      }
      ts.forEachChild(node, visit);
    };
    if (fn.node.body) visit(fn.node.body);
  }
  resource(server, expression, scope) {
    let config;
    try { config = this.fields(expression.arguments[2], scope); } catch { return; }
    const meta = config._meta === undefined ? undefined : this.value(config._meta);
    if (!meta || typeof meta !== "object" || !Object.hasOwn(meta, resourceSchemaKey)) return;
    const uri = this.expression(expression.arguments[1], scope);
    if (typeof uri !== "string" || server.resources.has(uri)) this.fail(expression, "Typed resource registration must have one unique constant URI.");
    if (this.value(config.mimeType) !== "application/json") this.fail(expression, `Typed resource ${uri} must declare the application/json mimeType.`);
    server.resources.set(uri, { uri, outputSchema: this.materialize(meta[resourceSchemaKey]) });
  }
  statementExpression(expression, scope) {
      if (ts.isAwaitExpression(expression)) expression = expression.expression;
      if (ts.isBinaryExpression(expression) && expression.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && expression.operatorToken.kind <= ts.SyntaxKind.LastAssignment) {
        const target = expression.left;
        let receiver;
        try {
          if (ts.isPropertyAccessExpression(target) || ts.isElementAccessExpression(target)) receiver = this.expression(target.expression, scope);
        } catch {}
        if (receiver instanceof Builtin && receiver.module !== sdk && !zodModules.has(receiver.module) && !this.containsRegistration(expression, scope) && !this.containsContractData(expression, scope)) return;
        this.fail(expression, "Mutation in server registration code cannot establish a static catalog.");
      }
      if (!ts.isCallExpression(expression)) {
        if (this.containsRegistration(expression, scope)) this.fail(expression, "Unsupported registration expression.");
        return;
      }
      const method = expression.expression;
      if (ts.isPropertyAccessExpression(method) || ts.isElementAccessExpression(method)) {
        const methodName = ts.isPropertyAccessExpression(method) ? method.name.text : this.expression(method.argumentExpression, scope);
        let receiver;
        try { receiver = this.expression(method.expression, scope); } catch {
          if (this.containsRegistration(expression, scope)) this.fail(expression, "Cannot resolve the receiver of a tool registration.");
          if (this.containsContractData(expression, scope)) this.fail(expression, "Schema data escapes static analysis.");
          return;
        }
        if (!(receiver instanceof Server)) {
          if (receiver instanceof Schema || Array.isArray(receiver) || receiver && typeof receiver === "object" && !(receiver instanceof Builtin)) this.fail(expression, "Mutation of schema or catalog data is unsupported.");
          if (this.containsRegistration(expression, scope)) this.fail(expression, "Server escape or deferred registration is unsupported.");
          if (this.containsContractData(expression, scope)) this.fail(expression, "Schema data escapes static analysis.");
          return;
        }
        if (methodName === "registerTool") {
          const name = this.expression(expression.arguments[0], scope);
          if (typeof name !== "string" || receiver.tools.has(name) || receiver.list !== undefined) this.fail(expression, "Tool registration must have one unique constant name and catalog source.");
          const config = this.fields(expression.arguments[1], scope);
          if (this.containsRegistration(expression.arguments[2], scope)) this.fail(expression.arguments[2], "Tool handlers must not change the tool catalog.");
          const tool = { name, inputSchema: this.schema(config.inputSchema, "input", expression) };
          if (config.outputSchema) tool.outputSchema = this.schema(config.outputSchema, "output", expression);
          receiver.tools.set(name, tool);
        } else if (methodName === "setRequestHandler") {
          const name = this.expression(expression.arguments[0], scope);
          if (name !== "tools/list") {
            if (expression.arguments.slice(1).some(arg => this.containsRegistration(arg, scope))) this.fail(expression, "Request handlers must not change the tool catalog.");
            return;
          }
          if (receiver.tools.size || receiver.list !== undefined) this.fail(expression, "Multiple tool catalog definitions are unsupported.");
          const fn = this.expression(expression.arguments.at(-1), scope);
          const result = this.functionBody(fn, [], expression, true);
          if (result.nextCursor) this.fail(expression, "A paginated tool catalog is not a fixed complete catalog.");
          const tools = this.value(result.tools);
          if (!Array.isArray(tools)) this.fail(expression, "tools/list must return a fixed tools array.");
          receiver.list = tools.map(t => {
            const object = this.value(t);
            const tool = { name: this.value(object.name), inputSchema: this.materialize(object.inputSchema) };
            if (object.outputSchema) tool.outputSchema = this.materialize(object.outputSchema);
            return tool;
          });
        } else {
          if (!["registerResource", "registerPrompt", "registerResourceTemplate"].includes(methodName)) this.fail(expression, `Server mutation ${methodName} is unsupported during static extraction.`);
          if (expression.arguments.some(arg => this.containsRegistration(arg, scope))) this.fail(expression, "Resource and prompt handlers must not change the tool catalog.");
          if (methodName === "registerResource") this.resource(receiver, expression, scope);
        }
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
