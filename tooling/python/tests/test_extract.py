from textwrap import dedent

import pytest
from jsonschema import Draft202012Validator, ValidationError

from extract import extract


@pytest.mark.parametrize(
    ("definition", "diagnostic"),
    [
        (
            """
            if True:
                @server.tool()
                def read(id: str) -> str:
                    return id
            """,
            "Register tools only with top-level MCP decorators or add_tool",
        ),
        (
            """
            @server.tool(name=os.getenv("NAME"))
            def read(id: str) -> str:
                return id
            """,
            "Expected a literal contract value",
        ),
        (
            """
            @server.tool()
            def read(id: str = factory()) -> str:
                return id
            """,
            "Only static pydantic.Field metadata is supported",
        ),
        (
            """
            NAME = "before"
            @server.tool(name=NAME)
            def read(id: str) -> str:
                return id
            NAME = "after"
            """,
            "Reassigned contract symbols",
        ),
        (
            """
            NAME = "before"
            NAME += "after"
            @server.tool(name=NAME)
            def read(id: str) -> str:
                return id
            """,
            "Reassigned contract symbols",
        ),
        (
            """
            other = MCPServer("other")
            @other.tool()
            def read(id: str) -> str:
                return id
            """,
            "must expose one official MCP server as server",
        ),
        (
            'getattr(server, "add_tool")(unknown)',
            "Passing the server to other code",
        ),
        (
            """
            def register():
                @server.tool()
                def read(id: str) -> str:
                    return id
            register()
            """,
            "Module-level helper execution",
        ),
        (
            """
            class Value(BaseModel):
                total: int
            if True:
                Value.model_fields["total"] = unknown
            @server.tool()
            def read(id: str) -> Value:
                raise RuntimeError()
            """,
            "Conditional or computed module assignments",
        ),
        (
            """
            class Value(BaseModel):
                total: int
                @field_validator("total")
                def convert(cls, value):
                    return value
            @server.tool()
            def read(id: str) -> Value:
                raise RuntimeError()
            """,
            "Private fields, methods, validators and computed fields",
        ),
        (
            """
            @server.tool()
            def read(value: "__import__('os').getcwd()") -> str:
                return value
            """,
            "Schema cannot be established statically",
        ),
        (
            """
            class Value(BaseModel):
                total: int = Field(default_factory=factory)
            @server.tool()
            def read() -> Value:
                raise RuntimeError()
            """,
            "Expected a literal contract value",
        ),
        (
            """
            class Value(BaseModel, metaclass=factory):
                total: int
            @server.tool()
            def read() -> Value:
                raise RuntimeError()
            """,
            "Expected a literal contract value",
        ),
    ],
)
def test_unprovable_contracts_fail_visibly(tmp_path, definition, diagnostic):
    (tmp_path / "server.py").write_text(
        dedent("""
            from mcp.server import MCPServer
            from pydantic import BaseModel, Field, field_validator
            server = MCPServer("test")
        """)
        + dedent(definition)
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
        dedent(f"""
            from mcp.server import MCPServer, Server
            server = {construction}
        """)
    )
    result = extract(tmp_path, "server.py")
    assert result["tools"] == []
    assert result["diagnostic"].startswith("server.py:")
    assert "Register tools only with top-level MCP decorators" in result["diagnostic"]


def test_fixed_catalog_uses_json_schema(tmp_path):
    (tmp_path / "server.py").write_text(
        dedent("""
            from mcp.server import Server
            from mcp.types import Tool
            INPUT = {"type": "object", "properties": {"id": {"type": "string"}}, "required": ["id"]}
            server = Server("test")
            @server.list_tools()
            async def tools():
                return [Tool(name="read", input_schema=INPUT, output_schema={"type": "object"})]
        """)
    )
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
    (tmp_path / "models.py").write_text(
        dedent("""
            from pydantic import BaseModel
            class Line(BaseModel):
                name: str
            class Order(Line):
                total: float
                lines: list[Line]
        """)
    )
    (tmp_path / "tools").mkdir()
    (tmp_path / "tools" / "__init__.py").write_text("")
    (tmp_path / "tools" / "lines.py").write_text(
        dedent("""
            from models import Line
            async def add_line(name: str) -> Line:
                raise RuntimeError("handler not called")
        """)
    )
    (tmp_path / "server.py").write_text(
        dedent("""
            from typing import Annotated
            from pathlib import Path
            from mcp.server import MCPServer
            from pydantic import Field
            from models import Order
            from tools import lines
            Path(__file__).with_name("executed").write_text("")
            server = MCPServer("orders")
            server.add_tool(lines.add_line)
            @server.tool("lookup")
            async def read(id: Annotated[str, Field(pattern="^ord_")], limit: int = 5) -> Order:
                raise RuntimeError("handler not called")
            @server.tool()
            async def unknown(id: str):
                return {"guessed": "never"}
        """)
    )
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
    inputs.validate({"id": "ord_1"})
    for payload in [{}, {"id": "invalid"}]:
        with pytest.raises(ValidationError):
            inputs.validate(payload)
    output = Draft202012Validator(tools["lookup"]["outputSchema"])
    output.validate({"name": "order", "total": 1.5, "lines": [{"name": "item"}]})
    with pytest.raises(ValidationError):
        output.validate({"total": 1.5, "lines": [{"name": "item"}]})


def test_only_typed_json_resources_advertise_contents_schemas(tmp_path):
    (tmp_path / "server.py").write_text(
        dedent("""
            from typing import TypedDict
            from mcp.server import MCPServer
            from mcp.server.apps import APP_MIME_TYPE
            server = MCPServer("chat")
            class Message(TypedDict):
                id: str
            messages_uri = "chat://messages"
            @server.resource(messages_uri, name="Messages", mime_type="application/json")
            def read_messages() -> dict[int, Message]:
                return {}
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
    )
    extracted = extract(tmp_path, "server.py")
    assert "diagnostic" not in extracted, extracted
    (resource,) = extracted["resources"]
    assert resource["uri"] == "chat://messages"
    validator = Draft202012Validator(resource["outputSchema"])
    validator.validate({"1": {"id": "message-1"}})
    with pytest.raises(ValidationError):
        validator.validate({"1": {"id": 1}})
