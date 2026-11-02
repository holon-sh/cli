import { join, relative } from "node:path";
import ts from "typescript";

const options = { strict: true, allowJs: true, moduleResolution: ts.ModuleResolutionKind.Bundler, lib: ["lib.esnext.d.ts"], types: [] };

export function functionContract(root, entry) {
  const file = join(root, entry);
  const program = ts.createProgram([file], options);
  const checker = program.getTypeChecker();
  const app = ts.resolveModuleName("holon-sh/app", file, options, ts.sys).resolvedModule?.resolvedFileName;
  const tools = [], resources = [];
  const fail = (node, message) => {
    const source = node.getSourceFile();
    const { line, character } = source.getLineAndCharacterOfPosition(node.getStart());
    throw new Error(`${relative(root, source.fileName)}:${line + 1}:${character + 1}: ${message}`);
  };
  const declarations = (call, key) => {
    const symbol = checker.getTypeAtLocation(call.arguments[1]).getProperty(key);
    return (symbol ? checker.getTypeOfSymbol(symbol).getProperties() : []).map(property => [property, checker.getTypeArguments(checker.getTypeOfSymbol(property)), property.valueDeclaration ?? call]);
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
  const uri = property => {
    const assigned = property.valueDeclaration;
    let value = ts.isShorthandPropertyAssignment(assigned) ? checker.getShorthandAssignmentValueSymbol(assigned)?.valueDeclaration?.initializer : assigned.initializer;
    if (value && ts.isIdentifier(value)) value = checker.getSymbolAtLocation(value)?.valueDeclaration?.initializer;
    const type = value && ts.isCallExpression(value) && value.arguments[0] && checker.getTypeAtLocation(value.arguments[0]);
    return type?.isStringLiteral() ? type.value : fail(assigned, `resource ${property.name} needs a string constant URI`);
  };
  const visit = node => {
    const callee = ts.isCallExpression(node) && node.arguments.length > 1 ? checker.getTypeAtLocation(node.expression).symbol : undefined;
    if (callee?.name === "register" && callee.declarations?.[0]?.getSourceFile().fileName === app) {
      for (const [property, [input, output], at] of declarations(node, "tools")) {
        const inputSchema = input.flags & ts.TypeFlags.Unknown ? { type: "object", properties: {}, additionalProperties: false } : object(input, at);
        tools.push({ name: property.name, inputSchema, outputSchema: object(output, at) });
      }
      for (const [property, [output], at] of declarations(node, "resources")) resources.push({ uri: uri(property), outputSchema: schema(output, at) });
    }
    ts.forEachChild(node, visit);
  };
  const files = program.getSourceFiles().filter(source => !source.isDeclarationFile && !program.isSourceFileFromExternalLibrary(source));
  for (const source of files) visit(source);
  return { contract: { schema: { tools, resources } }, files: files.map(source => source.fileName) };
}
