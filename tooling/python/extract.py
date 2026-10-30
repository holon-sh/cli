from __future__ import annotations

import ast
import hashlib
import inspect
import json
import os
import sys
import types
from collections.abc import Iterator, Mapping
from contextlib import contextmanager
from pathlib import Path
from typing import Any, Never, cast

import typing_extensions as typing
from mcp.server import MCPServer
from mcp.server.mcpserver import Context
from mcp.server.mcpserver.exceptions import InvalidSignature
from mcp.server.mcpserver.tools import Tool as SDKTool
from mcp.types import CallToolResult, InputRequiredResult, Tool, ToolAnnotations
from pydantic import BaseModel, Field, TypeAdapter
from pydantic.errors import PydanticUserError
from pydantic.fields import FieldInfo
from pydantic_core import SchemaError

IGNORED = frozenset(
    {
        ".git",
        ".venv",
        "venv",
        "node_modules",
        "holon",
        ".holon",
        ".holon-types",
        "__pycache__",
    }
)
ALIASES = {
    "typing_extensions": "typing",
    "mcp.server.mcpserver": "mcp.server",
    "mcp.server.lowlevel": "mcp.server",
}
SERVERS = {"mcp.server:MCPServer", "mcp.server:Server", "holon.app:App"}
REGISTRATIONS = {"tool", "add_tool", "list_tools", "remove_tool"}
CONSTRUCTOR_CATALOGS = {"tools", "on_list_tools", None}
FUNCTIONS = (ast.FunctionDef, ast.AsyncFunctionDef)
ASSIGNMENTS = (ast.Assign, ast.AnnAssign, ast.AugAssign)
# Only these trusted types may reach the SDK. Application symbols are resolved
# from their AST; no application module, annotation or callback is evaluated.
TYPES: dict[object, Any] = {
    "builtins:str": str,
    "builtins:int": int,
    "builtins:float": float,
    "builtins:bool": bool,
    "builtins:None": type(None),
    "builtins:object": object,
    "builtins:list": list,
    "builtins:tuple": tuple,
    "builtins:dict": dict,
    "pydantic:BaseModel": BaseModel,
    "mcp.server:Context": Context,
    "mcp.types:CallToolResult": CallToolResult,
    "mcp.types:InputRequiredResult": InputRequiredResult,
    **{
        f"typing:{name}": getattr(typing, name)
        for name in (
            "Any",
            "Annotated",
            "NotRequired",
            "Required",
            "Literal",
            "Optional",
            "Union",
            "List",
            "Tuple",
            "Dict",
            "TypedDict",
        )
    },
}
DUPLICATE = "duplicate"


class Analyzer:
    def __init__(self, root: Path):
        self.root = root.resolve()
        self.trees: dict[Path, ast.Module] = {}
        self.modules: dict[Path, dict[str, ast.AST | str]] = {}
        self.active: set[tuple[Path, str]] = set()
        self.models: dict[tuple[Path, str], type[Any]] = {}

    def fail(self, path: Path, node: ast.AST, message: str) -> Never:
        raise ValueError(
            f"{path.relative_to(self.root)}:{getattr(node, 'lineno', 1)}:{getattr(node, 'col_offset', 0) + 1}: {message}"
        )

    @contextmanager
    def visiting(self, path: Path, node: ast.AST, name: str) -> Iterator[None]:
        if (path, name) in self.active:
            self.fail(path, node, "Recursive or cyclic contracts are unsupported.")
        self.active.add((path, name))
        try:
            yield
        finally:
            self.active.remove((path, name))

    def tree(self, path: Path) -> ast.Module:
        path = path.resolve()
        if path not in self.trees:
            self.trees[path] = ast.parse(
                path.read_bytes(), filename=str(path.relative_to(self.root))
            )
        return self.trees[path]

    def load(self, path: Path) -> dict[str, ast.AST | str]:
        if path not in self.modules:
            symbols = self.modules[path] = {}
            for node in self.tree(path).body:
                for name, value in self.bindings(path, node):
                    symbols[name] = DUPLICATE if name in symbols else value
        return self.modules[path]

    def bindings(self, path: Path, node: ast.stmt) -> list[tuple[str, ast.AST | str]]:
        match node:
            case ast.ImportFrom(module=module, level=level, names=names):
                prefix = "." * level + (module or "")
                return [
                    (alias.asname or alias.name, f"{prefix}:{alias.name}")
                    for alias in names
                ]
            case ast.Import(names=names):
                return [
                    (
                        name := alias.asname or alias.name.split(".")[0],
                        f"{alias.name if alias.asname else name}:",
                    )
                    for alias in names
                ]
            case (
                ast.ClassDef(name=name)
                | ast.FunctionDef(name=name)
                | ast.AsyncFunctionDef(name=name)
            ):
                return [(name, node)]
            case ast.Assign() | ast.AnnAssign() | ast.AugAssign():
                targets = (
                    node.targets if isinstance(node, ast.Assign) else [node.target]
                )
                if any(
                    isinstance(target, (ast.Attribute, ast.Subscript))
                    for target in targets
                ):
                    self.fail(
                        path,
                        node,
                        "Module-level schema or catalog mutation is unsupported.",
                    )
                value = node if isinstance(node, ast.AugAssign) else node.value
                if value is None:
                    return []
                return [
                    (target.id, value)
                    for target in targets
                    if isinstance(target, ast.Name)
                ]
        return []

    def resolve(self, path: Path, node: ast.AST) -> tuple[Path, ast.AST | str]:
        match node:
            case ast.Constant(value=None):
                return path, "builtins:None"
            case ast.Constant(value=str() as text):
                return self.resolve(path, ast.parse(text, mode="eval").body)
            case ast.Attribute(value=base, attr=attr):
                source, value = self.resolve(path, base)
                if isinstance(value, str):
                    return self.imported(
                        source, value.replace(":", ".").rstrip(".") + ":" + attr
                    )
                return source, node
            case ast.Name(id=name):
                value = self.load(path).get(name, "builtins:" + name)
                if value == DUPLICATE:
                    self.fail(
                        path,
                        node,
                        "Reassigned contract symbols cannot be established statically.",
                    )
                with self.visiting(path, node, name):
                    if isinstance(value, str):
                        return self.imported(path, value)
                    if isinstance(value, (ast.Name, ast.Attribute)):
                        return self.resolve(path, value)
                    return path, value
        return path, node

    def imported(self, path: Path, symbol: str) -> tuple[Path, ast.AST | str]:
        module, _, name = symbol.rpartition(":")
        level = len(module) - len(module.lstrip("."))
        base = path.parent.joinpath(*[".."] * (level - 1)) if level else self.root
        parts = module.lstrip(".").split(".")
        if not level and parts[0] in IGNORED:
            return path, f"{module}:{name}"
        local = base.joinpath(*parts).with_suffix(".py")
        if not local.is_file():
            local = base.joinpath(*parts, "__init__.py")
        local = local.resolve()
        submodule = base.joinpath(*parts, name).with_suffix(".py").resolve()
        if name and submodule.is_file() and submodule.is_relative_to(self.root):
            return path, f"{module}.{name}:"
        if name and local.is_file() and local.is_relative_to(self.root):
            return self.resolve(local, ast.Name(id=name))
        return path, f"{ALIASES.get(module, module)}:{name}"

    def is_server(self, path: Path, node: ast.AST) -> bool:
        source, value = self.resolve(path, node)
        return (
            isinstance(value, ast.Call)
            and self.resolve(source, value.func)[1] in SERVERS
        )

    def constant(self, path: Path, node: ast.AST) -> Any:
        if isinstance(node, ast.Constant):
            return node.value
        _, target = self.resolve(path, node)
        return target.value if isinstance(target, ast.Constant) else None

    def literal(self, path: Path, node: ast.AST) -> Any:
        match node:
            case ast.Constant(value=constant):
                return constant
            case ast.JoinedStr(values=values):
                return "".join(str(self.literal(path, value)) for value in values)
            case ast.FormattedValue(value=value, conversion=-1, format_spec=None):
                formatted = self.literal(path, value)
                if not isinstance(formatted, (str, int, float)):
                    self.fail(
                        path,
                        node,
                        "Template values must be constant strings or numbers.",
                    )
                return formatted
            case ast.UnaryOp(op=ast.USub(), operand=operand):
                return -self.literal(path, operand)
            case ast.List(elts=items) | ast.Tuple(elts=items):
                return [self.literal(path, item) for item in items]
            case ast.Dict(keys=keys, values=values):
                result: dict[Any, Any] = {}
                for key, item in zip(keys, values):
                    result.update(
                        self.literal(path, item)
                        if key is None
                        else {self.literal(path, key): self.literal(path, item)}
                    )
                return result
        source, target = self.resolve(path, node)
        if isinstance(target, ast.AST) and target is not node:
            return self.literal(source, target)
        return self.fail(
            path,
            node,
            "Expected a literal contract value; application code is never evaluated.",
        )

    def annotation(self, path: Path, node: ast.AST) -> Any:
        source, origin = self.resolve(path, node)
        args = []
        if isinstance(origin, ast.Subscript):
            args = (
                origin.slice.elts
                if isinstance(origin.slice, ast.Tuple)
                else [origin.slice]
            )
            _, origin = self.resolve(source, origin.value)
        if isinstance(origin, ast.ClassDef):
            return self.model(source, origin)
        if isinstance(origin, ast.BinOp) and isinstance(origin.op, ast.BitOr):
            return self.annotation(source, origin.left) | self.annotation(
                source, origin.right
            )
        if isinstance(origin, ast.Constant) and origin.value is Ellipsis:
            return Ellipsis
        if origin not in TYPES:
            self.fail(path, node, "Schema cannot be established statically.")
        target = TYPES[origin]
        if not args:
            return target
        if target is typing.Literal:
            return target[tuple(self.literal(source, arg) for arg in args)]
        parameters = tuple(
            self.field(source, arg)
            if isinstance(arg, ast.Call)
            else self.annotation(source, arg)
            for arg in args
        )
        return target[parameters[0] if len(parameters) == 1 else parameters]

    def model(self, path: Path, value: ast.ClassDef) -> type[Any]:
        key = (path, value.name)
        if key in self.models:
            return self.models[key]
        if value.decorator_list or len(value.bases) != 1:
            self.fail(
                path, value, "Use an undecorated BaseModel or TypedDict contract."
            )
        with self.visiting(path, value, value.name):
            base = self.annotation(path, value.bases[0])
            if not (
                base is typing.TypedDict
                or typing.is_typeddict(base)
                or isinstance(base, type)
                and issubclass(base, BaseModel)
            ):
                self.fail(
                    path, value, "Use an undecorated BaseModel or TypedDict contract."
                )
            annotations: dict[str, Any] = {}
            namespace: dict[str, Any] = {
                "__annotations__": annotations,
                "__module__": __name__,
            }
            for member in value.body:
                match member:
                    case ast.AnnAssign(
                        target=ast.Name(id=name), annotation=annotation
                    ) if not name.startswith("_"):
                        annotations[name] = self.annotation(path, annotation)
                        if member.value is not None:
                            namespace[name] = self.default(path, member.value)
                    case ast.Assign(targets=[ast.Name(id="model_config")]):
                        namespace["model_config"] = self.literal(path, member.value)
                    case ast.Pass() | ast.Expr(value=ast.Constant()):
                        pass
                    case _:
                        self.fail(
                            path,
                            member,
                            "Private fields, methods, validators and computed fields are unsupported.",
                        )
            keywords = {
                item.arg: self.literal(path, item.value) for item in value.keywords
            }
            # types.new_class invokes only the trusted BaseModel/TypedDict machinery
            # with this inert namespace, never the original class body or decorators.
            model = types.new_class(
                value.name,
                (base,),
                cast(dict[str, Any], keywords),
                lambda ns: ns.update(namespace),
            )
            self.models[key] = model
            return model

    def field(self, path: Path, node: ast.AST) -> FieldInfo:
        if (
            not isinstance(node, ast.Call)
            or self.resolve(path, node.func)[1] != "pydantic:Field"
        ):
            self.fail(path, node, "Only static pydantic.Field metadata is supported.")
        return cast(
            FieldInfo,
            Field(
                *(self.literal(path, arg) for arg in node.args),
                **{
                    cast(str, item.arg): self.literal(path, item.value)
                    for item in node.keywords
                },
            ),
        )

    def default(self, path: Path, node: ast.AST) -> Any:
        return (
            self.field(path, node)
            if isinstance(node, ast.Call)
            else self.literal(path, node)
        )

    def tool(
        self,
        path: Path,
        function: ast.FunctionDef | ast.AsyncFunctionDef,
        options: Mapping[str, ast.AST],
    ) -> dict[str, Any]:
        name = TypeAdapter(str | None).validate_python(
            self.literal(path, options.get("name", ast.Constant(None)))
        )
        signature = function.args
        if signature.posonlyargs or signature.vararg or signature.kwarg:
            self.fail(
                path,
                function,
                "Variadic and positional-only tool parameters are unsupported.",
            )
        defaults = (
            [None] * (len(signature.args) - len(signature.defaults))
            + signature.defaults
            + signature.kw_defaults
        )
        parameters = []
        for arg, default in zip(signature.args + signature.kwonlyargs, defaults):
            if arg.annotation is None:
                self.fail(
                    path, arg, "Typed tool parameters require explicit annotations."
                )
            parameters.append(
                inspect.Parameter(
                    arg.arg,
                    inspect.Parameter.KEYWORD_ONLY,
                    annotation=self.annotation(path, arg.annotation),
                    default=inspect.Parameter.empty
                    if default is None
                    else self.default(path, default),
                )
            )

        def stub() -> Never:
            raise AssertionError("Contract extraction must never call a tool.")

        stub.__name__ = function.name
        stub.__annotations__ = {param.name: param.annotation for param in parameters}
        structured = self.literal(
            path, options.get("structured_output", ast.Constant(None))
        )
        returns = inspect.Signature.empty
        if function.returns is not None and structured is not False:
            returns = self.annotation(path, function.returns)
            stub.__annotations__["return"] = returns
        # The SDK sees concrete trusted annotations and a signature, not source code.
        stub.__signature__ = inspect.Signature(parameters, return_annotation=returns)  # type: ignore[attr-defined]
        tool = SDKTool.from_function(stub, name=name, structured_output=structured)
        result = Tool(
            name=tool.name,
            input_schema=tool.parameters,
            output_schema=tool.output_schema,
        ).model_dump(by_alias=True, exclude_none=True)
        result["description"] = ast.get_docstring(function) or ""
        for key in ("title", "description"):
            if key in options:
                result[key] = self.literal(path, options[key])
        if "meta" in options:
            result["_meta"] = self.literal(path, options["meta"])
        if "changes" in options:
            changes = self.literal(path, options["changes"])
            if not isinstance(changes, (list, tuple)) or any(
                not isinstance(uri, str) or not uri for uri in changes
            ):
                self.fail(
                    path,
                    options["changes"],
                    "changes must contain resource URI strings",
                )
            result["_meta"] = {
                **(result.get("_meta") or {}),
                "sh.holon/changes": list(changes),
            }
        if "annotations" in options:
            node = options["annotations"]
            if (
                isinstance(node, ast.Call)
                and self.resolve(path, node.func)[1] == "mcp.types:ToolAnnotations"
            ):
                result["annotations"] = ToolAnnotations.model_validate(
                    {item.arg: self.literal(path, item.value) for item in node.keywords}
                ).model_dump(by_alias=True, exclude_none=True)
            else:
                result["annotations"] = self.literal(path, node)
        return result

    def registration(self, node: ast.AST, servers: set[str]) -> str | None:
        match node:
            case ast.Call(func=ast.Attribute(value=ast.Name(id=name), attr=attr)) if (
                name in servers
            ):
                return attr
        return None

    def decorated(
        self,
        path: Path,
        function: ast.FunctionDef | ast.AsyncFunctionDef,
        call: ast.Call,
    ) -> list[dict[str, Any]]:
        signature = inspect.signature(MCPServer.tool)
        keywords = {cast(str, item.arg): item.value for item in call.keywords}
        changes = keywords.pop("changes", None)
        if changes is not None and isinstance(call.func, ast.Attribute):
            source, construction = self.resolve(path, call.func.value)
            if (
                not isinstance(construction, ast.Call)
                or self.resolve(source, construction.func)[1] != "holon.app:App"
            ):
                self.fail(path, changes, "changes is supported only by holon.app.App")
        options = signature.bind(
            None,
            *call.args,
            **keywords,
        ).arguments
        if changes is not None:
            options["changes"] = changes
        return [self.tool(path, function, options)]

    def resource(
        self,
        path: Path,
        function: ast.FunctionDef | ast.AsyncFunctionDef,
        call: ast.Call,
    ) -> list[dict[str, Any]]:
        signature = inspect.signature(MCPServer.resource)
        options = signature.bind(
            None,
            *call.args,
            **{cast(str, item.arg): item.value for item in call.keywords},
        ).arguments
        mime_type = self.constant(path, options.get("mime_type", ast.Constant(None)))
        if mime_type != "application/json" or function.returns is None:
            return []
        uri = self.constant(path, options.get("uri", ast.Constant(None)))
        if not isinstance(uri, str) or not uri:
            self.fail(path, call, "Typed resources require a constant URI.")
        return [
            {
                "uri": uri,
                "outputSchema": TypeAdapter(
                    self.annotation(path, function.returns)
                ).json_schema(),
            }
        ]

    def catalog(
        self,
        path: Path,
        function: ast.FunctionDef | ast.AsyncFunctionDef,
        call: ast.Call,
    ) -> list[dict[str, Any]]:
        body = (
            function.body[1:]
            if ast.get_docstring(function) is not None
            else function.body
        )
        match body:
            case [ast.Return(value=ast.List(elts=items) | ast.Tuple(elts=items))]:
                return [self.listed(path, item) for item in items]
        return self.fail(
            path, function, "tools/list must return a fixed literal catalog."
        )

    def listed(self, path: Path, item: ast.expr) -> dict[str, Any]:
        match item:
            case ast.Call(func=func, args=[], keywords=keywords) if (
                self.resolve(path, func)[1] == "mcp.types:Tool"
            ):
                tool = Tool(
                    **{
                        cast(str, keyword.arg): self.literal(path, keyword.value)
                        for keyword in keywords
                    }
                )
                return tool.model_dump(
                    by_alias=True,
                    exclude_none=True,
                    include={"name", "input_schema", "output_schema"},
                )
        return self.fail(
            path,
            item,
            "Expected a literal official MCP Tool with name and input_schema.",
        )

    def added(self, path: Path, call: ast.Call) -> list[dict[str, Any]]:
        match call:
            case ast.Call(args=[argument], keywords=[]):
                source, value = self.resolve(path, argument)
                if isinstance(value, FUNCTIONS) and not value.decorator_list:
                    return [self.tool(source, value, {})]
        return self.fail(
            path,
            call,
            "add_tool requires one statically resolvable undecorated function.",
        )

    def extract(self, path: Path, descriptor: bool = False) -> dict[str, Any]:
        tree = self.tree(path)
        servers = {
            name for name in self.load(path) if self.is_server(path, ast.Name(id=name))
        }
        if (
            "server" not in servers
            or len({self.resolve(path, ast.Name(id=name))[1] for name in servers}) != 1
        ):
            self.fail(
                path,
                tree,
                "The entrypoint must expose one official MCP server as server.",
            )
        if descriptor:
            source, construction = self.resolve(path, ast.Name(id="server"))
            assert isinstance(construction, ast.Call)
            if len(construction.args) > 1 or any(
                item.arg is None for item in construction.keywords
            ):
                self.fail(
                    source,
                    construction,
                    "Declare metadata with explicit MCP constructor arguments.",
                )
            fields = {"name", "version", "description", "title", "website_url", "icons"}
            info = {
                item.arg: self.literal(source, item.value)
                for item in construction.keywords
                if item.arg in fields
            }
            if construction.args:
                info["name"] = self.literal(source, construction.args[0])
            if "website_url" in info:
                info["websiteUrl"] = info.pop("website_url")
            publishing = (
                self.literal(path, ast.Name(id="publishing"))
                if "publishing" in self.load(path)
                else {}
            )
            if not isinstance(publishing, dict):
                self.fail(
                    path,
                    construction,
                    "Declare publishing as a dictionary literal.",
                )
            if fields.intersection(publishing) or "websiteUrl" in publishing:
                self.fail(
                    path,
                    construction,
                    "Declare shared metadata in the MCP constructor, not publishing.",
                )
            return {"descriptor": {**publishing, **info}}
        match self.resolve(path, ast.Name(id="server")):
            case (source, ast.Call(keywords=keywords) as construction) if any(
                item.arg in CONSTRUCTOR_CATALOGS for item in keywords
            ):
                self.fail(
                    source,
                    construction,
                    "Register tools only with top-level MCP decorators or add_tool in the entrypoint.",
                )
        tools: list[dict[str, Any]] = []
        resources: list[dict[str, Any]] = []
        handled = set()
        for node in tree.body:
            match node:
                case (
                    ast.FunctionDef(decorator_list=[ast.Call() as call])
                    | ast.AsyncFunctionDef(decorator_list=[ast.Call() as call])
                ) if (kind := self.registration(call, servers)) in DECORATORS:
                    handled.add(call.func)
                    registered = resources if kind == "resource" else tools
                    registered += DECORATORS[kind](self, path, node, call)
                case ast.Expr(value=ast.Call() as call) if (
                    self.registration(call, servers) == "add_tool"
                ):
                    handled.add(call.func)
                    tools += self.added(path, call)
                case ast.Expr(value=ast.Call(func=func)) if isinstance(
                    self.resolve(path, func)[1], FUNCTIONS
                ):
                    self.fail(
                        path,
                        node,
                        "Module-level helper execution cannot establish a static catalog.",
                    )
                case ast.If() | ast.For() | ast.While() | ast.With() | ast.Try() if any(
                    isinstance(item, ASSIGNMENTS) for item in ast.walk(node)
                ):
                    self.fail(
                        path,
                        node,
                        "Conditional or computed module assignments are unsupported.",
                    )
        for child in ast.walk(tree):
            match child:
                case ast.Call(args=args, keywords=keywords) if any(
                    isinstance(item, ast.Name) and item.id in servers
                    for item in [*args, *(keyword.value for keyword in keywords)]
                ):
                    self.fail(
                        path,
                        child,
                        "Passing the server to other code cannot establish a static contract.",
                    )
        for source, module in list(self.trees.items()):
            for child in ast.walk(module):
                if (
                    isinstance(child, ast.Attribute)
                    and child.attr in REGISTRATIONS
                    and child not in handled
                    and self.is_server(source, child.value)
                ):
                    self.fail(
                        source,
                        child,
                        "Register tools only with top-level MCP decorators or add_tool in the entrypoint.",
                    )
        names = [tool["name"] for tool in tools]
        uris = [resource["uri"] for resource in resources]
        if len(names) != len(set(names)) or len(uris) != len(set(uris)):
            self.fail(path, tree, "Duplicate tool registrations are unsupported.")
        return {"tools": tools, "resources": resources}


def extract(root: str | Path, entry: str, descriptor: bool = False) -> dict[str, Any]:
    analyzer = Analyzer(Path(root))
    sources: dict[str, str] = {}
    try:
        for directory, folders, filenames in os.walk(analyzer.root):
            folders[:] = [name for name in folders if name not in IGNORED]
            for name in filenames:
                if name.endswith(".py"):
                    path = Path(directory, name)
                    with path.open("rb") as source:
                        sources[str(path.relative_to(analyzer.root))] = (
                            hashlib.file_digest(source, "sha256").hexdigest()
                        )
        catalog = analyzer.extract((analyzer.root / entry).resolve(), descriptor)
        return {**catalog, "sources": sources}
    except (
        ValueError,
        SyntaxError,
        OSError,
        LookupError,
        TypeError,
        RecursionError,
        PydanticUserError,
        InvalidSignature,
        SchemaError,
    ) as error:
        return {
            "tools": [],
            "diagnostic": str(error),
            "sources": sources,
        }


DECORATORS = {
    "tool": Analyzer.decorated,
    "list_tools": Analyzer.catalog,
    "resource": Analyzer.resource,
}


if __name__ == "__main__":
    # extract.py CONFIG OUTPUT: contracts for every app in the config, as JSON.
    config, output = sys.argv[1:]
    apps = json.loads(Path(config).read_text())["apps"]
    Path(output).write_text(
        json.dumps(
            {
                app["name"]: extract(
                    app["path"], app["entry"], app.get("descriptor", False)
                )
                for app in apps
            }
        )
    )
