import { join, relative } from "node:path";
import ts from "typescript";
import { compilerOptions } from "./sandbox.mjs";

export function functionContract(root, entry) {
  const file = join(root, entry);
  const options = { ...compilerOptions(root), strict: true, allowJs: true, moduleResolution: ts.ModuleResolutionKind.Bundler, lib: ["lib.esnext.d.ts"], types: [] };
  const program = ts.createProgram([file], options);
  const checker = program.getTypeChecker();
  const app = from => ts.resolveModuleName("holon-sh/app", from, options, ts.sys).resolvedModule?.resolvedFileName;
  const tools = [], resources = [];
  const fail = (node, message) => {
    const source = node.getSourceFile();
    const { line, character } = source.getLineAndCharacterOfPosition(node.getStart());
    throw new Error(`${relative(root, source.fileName)}:${line + 1}:${character + 1}: ${message}`);
  };
  const listed = (type, at) => type.flags & ts.TypeFlags.Object && !checker.getIndexInfosOfType(type).length ? type.getProperties() : fail(at, "register's declarations must be listed statically");
  const declarations = (call, key) => {
    const group = listed(checker.getTypeAtLocation(call.arguments[1]), call.arguments[1]).find(symbol => symbol.name === key);
    return (group ? listed(checker.getTypeOfSymbol(group), group.valueDeclaration ?? call) : []).map(property => {
      const type = checker.getTypeOfSymbol(property);
      const [first = type, second = type] = checker.getTypeArguments(type);
      return [property.name, first, second, property.valueDeclaration ?? call];
    });
  };
  const schema = (type, at, seen = new Set()) => {
    if (type.isStringLiteral() || type.isNumberLiteral()) return { const: type.value };
    if (type.flags & ts.TypeFlags.BooleanLiteral) return { const: checker.typeToString(type) === "true" };
    if (type.flags & ts.TypeFlags.String) return { type: "string" };
    if (type.flags & ts.TypeFlags.Number) return { type: "number" };
    if (type.flags & ts.TypeFlags.Null) return { type: "null" };
    if (type.isUnion()) {
      const options = type.types.filter(option => !(option.flags & ts.TypeFlags.Undefined));
      if (options.every(option => option.flags & ts.TypeFlags.BooleanLiteral)) return { type: "boolean" };
      return options.length === 1 ? schema(options[0], at, seen) : { anyOf: options.map(option => schema(option, at, seen)) };
    }
    if (checker.isArrayType(type)) return { type: "array", items: schema(checker.getTypeArguments(type)[0], at, seen) };
    if (type.flags & ts.TypeFlags.Object && !type.getCallSignatures().length && !checker.isTupleType(type) && !checker.getIndexInfosOfType(type).length && !seen.has(type)) {
      const properties = {}, required = [], inside = new Set(seen).add(type);
      for (const property of type.getProperties()) {
        properties[property.name] = schema(checker.getTypeOfSymbol(property), at, inside);
        if (!(property.flags & ts.SymbolFlags.Optional)) required.push(property.name);
      }
      return { type: "object", properties, ...(required.length && { required }), additionalProperties: false };
    }
    fail(at, `${checker.typeToString(type)} has no JSON Schema; register this tool with schemas instead`);
  };
  const object = (type, at) => {
    const result = schema(type, at);
    return result.type === "object" ? result : fail(at, "tool inputs and results must be JSON objects");
  };
  const visit = node => {
    const callee = ts.isCallExpression(node) && node.arguments.length > 1 ? checker.getTypeAtLocation(node.expression).symbol : undefined;
    if (callee?.name === "register" && callee.declarations?.[0]?.getSourceFile().fileName === app(node.getSourceFile().fileName)) {
      for (const [name, input, output, at] of declarations(node, "tools")) tools.push({ name, inputSchema: object(input, at), outputSchema: object(output, at) });
      for (const [name, output, uri, at] of declarations(node, "resources")) resources.push({ uri: uri.isStringLiteral() ? uri.value : fail(at, `resource ${name} needs a string constant URI`), outputSchema: schema(output, at) });
    }
    ts.forEachChild(node, visit);
  };
  const files = program.getSourceFiles().filter(source => !source.isDeclarationFile && !program.isSourceFileFromExternalLibrary(source));
  for (const source of files) visit(source);
  return { schema: { tools, resources }, files: files.map(source => source.fileName) };
}
