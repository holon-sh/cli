import json
import subprocess
import sys

import pytest
from jsonschema import Draft202012Validator, ValidationError

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
            "Unsupported Field option; no callbacks are evaluated",
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


@pytest.mark.parametrize(
    "annotation",
    [
        "Order",
        "list[str]",
        "dict[str, float]",
        "Dict[str, float]",
        "Annotated[CallToolResult, Order]",
        "Order | InputRequiredResult",
        "Snapshot",
        'Union[Literal["open", "closed"], None]',
        "tuple[str, int]",
        "tuple[str, ...]",
        "CallToolResult",
        "InputRequiredResult",
    ],
)
def test_static_contract_matches_official_catalog_without_running_sources(
    tmp_path, annotation
):
    (tmp_path / "models.py").write_text("""from typing import Annotated
from typing_extensions import TypedDict, Required, NotRequired
from pydantic import BaseModel, Field
class Line(BaseModel):
    model_config = {"extra": "forbid"}
    name: str = Field(..., min_length=1)
class Order(Line):
    total: Annotated[float, Field(ge=0)]
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
    (
        tmp_path / "server.py"
    ).write_text(f"""from typing import Annotated, Dict, Literal, Optional, Union
from pathlib import Path
from mcp.server import MCPServer
from mcp.server.mcpserver import Context
from mcp.types import CallToolResult, InputRequiredResult
from pydantic import Field
from models import Order, Snapshot
from tools import lines
Path(__file__).with_name("executed").write_text("")
server = MCPServer("orders")
server.add_tool(lines.add_line)
@server.tool()
async def read(id: Annotated[str, Field(pattern="^ord_")], context: Context, limit: int = 5, *, order: Optional[Order] = None) -> {annotation}:
    raise RuntimeError("handler not called")
@server.tool()
async def unknown(id: str):
    return {{"guessed": "never"}}
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
    # Run this owned fixture in a separate interpreter so imported models cannot
    # leak between cases. Only the reference catalog executes the fixture.
    catalog = subprocess.check_output(
        [
            sys.executable,
            "-c",
            """import asyncio, json, server
print(json.dumps([tool.model_dump(by_alias=True, exclude_none=True,
    include={"name", "input_schema", "output_schema"})
    for tool in asyncio.run(server.server.list_tools())]))
""",
        ],
        cwd=tmp_path,
        text=True,
    )
    assert extracted["tools"] == json.loads(catalog)


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
    ("definition", "diagnostic"),
    [
        (
            "@server.tool(structured_output=True)\ndef read(): pass",
            "return annotation required",
        ),
        (
            'class Value(BaseModel):\n    name: str = Field(pattern="[")\n@server.tool()\ndef read(value: Value): pass',
            "regex parse error",
        ),
    ],
)
def test_library_schema_failures_are_reported(tmp_path, definition, diagnostic):
    (tmp_path / "server.py").write_text(
        'from mcp.server import MCPServer\nfrom pydantic import BaseModel, Field\nserver=MCPServer("test")\n'
        + definition
    )
    result = extract(tmp_path, "server.py")
    assert result["tools"] == []
    assert diagnostic in result["diagnostic"]


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
    Draft202012Validator.check_schema(resource["outputSchema"])
    validator = Draft202012Validator(resource["outputSchema"])
    validator.validate({"messages": [{"id": "message-1"}]})
    for payload, rule in [({}, "required"), ({"messages": [{"id": 1}]}, "type")]:
        with pytest.raises(ValidationError) as failure:
            validator.validate(payload)
        assert failure.value.validator == rule
