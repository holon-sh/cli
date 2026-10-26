import pytest
from jsonschema import Draft202012Validator, ValidationError
from mcp.server.mcpserver.exceptions import InvalidSignature
from mcp.server.mcpserver.tools import Tool as SDKTool
from pydantic_core import SchemaError

from extract import extract


@pytest.mark.parametrize(
    ("definition", "diagnostic"),
    [
        (
            "if True:\n    @server.tool()\n    def read(id: str) -> str: return id",
            "Register tools only with top-level MCP decorators or add_tool",
        ),
        (
            '@server.tool(name=os.getenv("NAME"))\ndef read(id: str) -> str: return id',
            "Expected a literal contract value",
        ),
        (
            "@server.tool()\ndef read(id: str = factory()) -> str: return id",
            "Only static pydantic.Field metadata is supported",
        ),
        (
            'NAME = "before"\n@server.tool(name=NAME)\ndef read(id: str) -> str: return id\nNAME = "after"',
            "Reassigned contract symbols",
        ),
        (
            'NAME = "before"\nNAME += "after"\n@server.tool(name=NAME)\ndef read(id: str) -> str: return id',
            "Reassigned contract symbols",
        ),
        (
            'other = MCPServer("other")\n@other.tool()\ndef read(id: str) -> str: return id',
            "must expose one official MCP server as server",
        ),
        (
            'getattr(server, "add_tool")(unknown)',
            "Passing the server to other code",
        ),
        (
            "def register():\n    @server.tool()\n    def read(id: str) -> str: return id\nregister()",
            "Module-level helper execution",
        ),
        (
            'class Value(BaseModel):\n    total: int\nif True:\n    Value.model_fields["total"] = unknown\n@server.tool()\ndef read(id: str) -> Value: raise RuntimeError()',
            "Conditional or computed module assignments",
        ),
        (
            'class Value(BaseModel):\n    total: int\n    @field_validator("total")\n    def convert(cls, value): return value\n@server.tool()\ndef read(id: str) -> Value: raise RuntimeError()',
            "Private fields, methods, validators and computed fields",
        ),
        (
            "@server.tool()\ndef read(value: \"__import__('os').getcwd()\") -> str: return value",
            "Schema cannot be established statically",
        ),
        (
            "class Value(BaseModel):\n    total: int = Field(default_factory=factory)\n@server.tool()\ndef read() -> Value: raise RuntimeError()",
            "Expected a literal contract value",
        ),
        (
            "class Value(BaseModel, metaclass=factory):\n    total: int\n@server.tool()\ndef read() -> Value: raise RuntimeError()",
            "Expected a literal contract value",
        ),
        (
            'class Value(BaseModel):\n    child: "Value"\n@server.tool()\ndef read() -> Value: raise RuntimeError()',
            "Recursive or cyclic contracts are unsupported",
        ),
    ],
)
def test_unprovable_contracts_fail_visibly(tmp_path, definition, diagnostic):
    (tmp_path / "server.py").write_text(
        'from mcp.server import MCPServer\nfrom pydantic import BaseModel, Field, field_validator\nserver=MCPServer("test")\n'
        + definition
    )
    result = extract(tmp_path, "server.py")
    assert result["tools"] == []
    assert result["diagnostic"].startswith("server.py:")
    assert diagnostic in result["diagnostic"]


@pytest.mark.parametrize(
    "construction",
    [
        'MCPServer("test", tools=TOOLS)',
        'Server("test", on_list_tools=list_tools)',
        "MCPServer(**OPTIONS)",
    ],
)
def test_constructor_catalogs_fail_visibly(tmp_path, construction):
    (tmp_path / "server.py").write_text(
        f"from mcp.server import MCPServer, Server\nserver = {construction}\n"
    )
    result = extract(tmp_path, "server.py")
    assert result["tools"] == []
    assert result["diagnostic"].startswith("server.py:")
    assert "Register tools only with top-level MCP decorators" in result["diagnostic"]


def test_fixed_catalog_uses_json_schema(tmp_path):
    (tmp_path / "server.py").write_text("""from mcp.server import Server
from mcp.types import Tool
INPUT = {"type": "object", "properties": {"id": {"type": "string"}}, "required": ["id"]}
server = Server("test")
@server.list_tools()
async def tools():
    return [Tool(name="read", input_schema=INPUT, output_schema={"type": "object"})]
""")
    result = extract(tmp_path, "server.py")
    assert "diagnostic" not in result, result
    assert result["tools"] == [
        {
            "name": "read",
            "inputSchema": {
                "type": "object",
                "properties": {"id": {"type": "string"}},
                "required": ["id"],
            },
            "outputSchema": {"type": "object"},
        }
    ]


def test_static_contract_preserves_imported_types_without_running_sources(tmp_path):
    (tmp_path / "models.py").write_text("""from typing import Literal
from typing_extensions import TypedDict, Required, NotRequired
from pydantic import BaseModel, Field
class Line(BaseModel, extra="forbid"):
    model_config = {"strict": True}
    name: str = Field(...)
class Order(Line):
    total: float
    status: Literal["open", "closed"] = "open"
    lines: list[Line] = []
class Partial(TypedDict, total=False):
    id: Required[str]
class Snapshot(Partial):
    lines: list[Line]
    note: NotRequired[str]
""")
    (tmp_path / "tools").mkdir()
    (tmp_path / "tools" / "__init__.py").write_text("")
    (tmp_path / "tools" / "lines.py").write_text(
        "from models import Line\nasync def add_line(name: str) -> Line:\n    raise RuntimeError('handler not called')\n"
    )
    (tmp_path / "server.py").write_text("""from typing import Annotated
from pathlib import Path
from mcp.server import MCPServer
from mcp.server.mcpserver import Context
from mcp.types import CallToolResult
from pydantic import Field
from models import Order, Snapshot
from tools import lines
Path(__file__).with_name("executed").write_text("")
server = MCPServer("orders")
server.add_tool(lines.add_line)
@server.tool("lookup", description="Read an order")
async def read(id: Annotated[str, Field(pattern="^ord_")], snapshot: Snapshot, context: Context, limit: int = 5, *, order: Order | None = None) -> Annotated[CallToolResult, Order]:
    raise RuntimeError("handler not called")
@server.tool()
async def unknown(id: str):
    return {"guessed": "never"}
""")
    extracted = extract(tmp_path, "server.py")
    assert "diagnostic" not in extracted, extracted
    assert set(extracted["sources"]) == {
        "server.py",
        "models.py",
        "tools/__init__.py",
        "tools/lines.py",
    }
    assert not (tmp_path / "executed").exists()
    tools = {tool["name"]: tool for tool in extracted["tools"]}
    assert set(tools) == {"add_line", "lookup", "unknown"}
    assert "outputSchema" not in tools["unknown"]
    inputs = Draft202012Validator(tools["lookup"]["inputSchema"])
    inputs.validate({"id": "ord_1", "snapshot": {"id": "snap_1", "lines": []}})
    inputs.validate(
        {
            "id": "ord_1",
            "snapshot": {"id": "snap_1", "lines": [{"name": "item"}], "note": "ready"},
            "limit": 2,
            "order": {"name": "order", "total": 1.5},
        }
    )
    for payload in [
        {"snapshot": {"id": "snap_1", "lines": []}},
        {"id": "ord_1", "snapshot": {"lines": []}},
        {"id": "ord_1", "snapshot": {"id": "snap_1"}},
        {"id": "invalid", "snapshot": {"id": "snap_1", "lines": []}},
    ]:
        with pytest.raises(ValidationError):
            inputs.validate(payload)
    output = Draft202012Validator(tools["lookup"]["outputSchema"])
    output.validate(
        {"name": "order", "total": 1.5, "status": "open", "lines": [{"name": "item"}]}
    )
    for payload in [
        {"total": 1.5},
        {"name": "order", "total": 1, "unexpected": True},
        {"name": "order", "total": 1, "status": "unknown"},
    ]:
        with pytest.raises(ValidationError):
            output.validate(payload)
    added_output = Draft202012Validator(tools["add_line"]["outputSchema"])
    added_output.validate({"name": "item"})
    with pytest.raises(ValidationError):
        added_output.validate({})


def test_disabled_structured_output_does_not_resolve_return_annotation(tmp_path):
    (tmp_path / "server.py").write_text("""from mcp.server import MCPServer
server = MCPServer("test")
@server.tool(structured_output=False)
def read() -> Unknown:
    raise RuntimeError("must not execute")
""")
    result = extract(tmp_path, "server.py")
    assert "diagnostic" not in result, result
    (tool,) = result["tools"]
    assert tool["name"] == "read"
    assert "outputSchema" not in tool


@pytest.mark.parametrize(
    "error", [InvalidSignature("invalid contract"), SchemaError("invalid contract")]
)
def test_library_failures_become_extraction_diagnostics(tmp_path, monkeypatch, error):
    (tmp_path / "server.py").write_text("""from mcp.server import MCPServer
server = MCPServer("test")
@server.tool()
def read(id: str) -> str: return id
""")

    def reject(*args, **kwargs):
        raise error

    monkeypatch.setattr(SDKTool, "from_function", reject)
    result = extract(tmp_path, "server.py")
    assert result["tools"] == []
    assert result["diagnostic"] == "invalid contract"


def test_only_typed_json_resources_advertise_contents_schemas(tmp_path):
    (tmp_path / "server.py").write_text("""from typing import TypedDict
from mcp.server import MCPServer
from mcp.server.apps import APP_MIME_TYPE
server = MCPServer("chat")
class Message(TypedDict):
    id: str
class Snapshot(TypedDict):
    messages: list[Message]
messages_uri = "chat://messages"
@server.resource(messages_uri, name="Messages", mime_type="application/json")
def read_messages() -> Snapshot:
    return {"messages": []}
@server.resource("chat://readme", mime_type="text/markdown")
def readme() -> str:
    return "# Chat"
@server.resource("chat://untyped", mime_type="application/json")
def untyped():
    return {}
@server.resource("ui://chat/app.html", mime_type=APP_MIME_TYPE, meta={"ui": {}})
def app() -> str:
    return "<html></html>"
""")
    extracted = extract(tmp_path, "server.py")
    assert "diagnostic" not in extracted, extracted
    (resource,) = extracted["resources"]
    assert resource["uri"] == "chat://messages"
    validator = Draft202012Validator(resource["outputSchema"])
    validator.validate({"messages": [{"id": "message-1"}]})
    with pytest.raises(ValidationError):
        validator.validate({"messages": [{"id": 1}]})
