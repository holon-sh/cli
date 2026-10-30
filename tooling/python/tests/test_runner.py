"""The bundled extractor imports the app and asks its own server for the catalog."""

import json
import subprocess
import sys
import zipfile
from pathlib import Path
from textwrap import dedent

ROOT = Path(__file__).resolve().parents[1]

MODELS = """
    from typing import Literal, TypedDict
    from pydantic import BaseModel

    class Order(BaseModel):
        id: str
        status: Literal["open", "closed"]

    class Summary(TypedDict):
        total: int
"""

SERVER = """
    import os
    from enum import Enum
    from mcp.server import MCPServer
    from models import Order, Summary

    # Read at import time: HOLON_CONTRACT lets applications skip start-up side effects.
    if os.environ.get("HOLON_CONTRACT") != "1":
        raise RuntimeError("started outside contract extraction")

    class Region(Enum):
        EU = "eu"
        US = "us"

    server = MCPServer("orders")

    @server.tool()
    def read(id: str, region: Region = Region.EU) -> Order:
        raise RuntimeError("must not be called")

    @server.tool()
    def summarize() -> Summary:
        raise RuntimeError("must not be called")

    ORDER = {"type": "object", "properties": {"id": {"type": "string"}}}

    @server.resource(
        "orders://latest",
        mime_type="application/json",
        meta={"sh.holon/outputSchema": ORDER},
    )
    def latest() -> Order:
        raise RuntimeError("must not be called")

    @server.resource("ui://orders/app.html", mime_type="text/html")
    def page() -> str:
        raise RuntimeError("must not be called")

    if __name__ == "__main__":
        raise RuntimeError("must not run as a script")
"""

LOW_LEVEL = """
    from mcp import types
    from mcp.server import Server
    from models import Order

    async def tools(context, params):
        return types.ListToolsResult(tools=[types.Tool(
            name="lookup",
            input_schema={"type": "object"},
            output_schema=Order.model_json_schema(),
        )])

    app = Server("low", on_list_tools=tools)
"""


def write(directory: Path, files: dict[str, str]) -> Path:
    directory.mkdir()
    for name, source in files.items():
        (directory / name).write_text(dedent(source))
    return directory


def extract(tmp_path: Path, apps: dict[str, Path]) -> dict[str, dict]:
    archive = Path(
        subprocess.check_output(
            [sys.executable, "-I", str(ROOT / "build.py")], text=True
        ).strip()
    )
    with zipfile.ZipFile(archive) as bundle:
        bundle.extractall(tmp_path / "tooling")
    config = tmp_path / "config.json"
    output = tmp_path / "output.json"
    entries = [
        {"name": n, "path": str(p), "entry": "server.py"} for n, p in apps.items()
    ]
    config.write_text(json.dumps({"apps": entries}))
    extractor = [sys.executable, "-I", str(tmp_path / "tooling/extract.py")]
    subprocess.run([*extractor, str(config), str(output)], check=True)
    return json.loads(output.read_text())


def test_bundle_extracts_the_servers_own_catalog_and_detects_changes(tmp_path):
    orders = write(tmp_path / "orders", {"server.py": SERVER, "models.py": MODELS})
    # Each app in a batch imports its own models module, although the names repeat.
    low = write(
        tmp_path / "low",
        {"server.py": LOW_LEVEL, "models.py": MODELS.replace("id: str", "sku: int")},
    )
    result = extract(tmp_path, {"orders": orders, "low": low})

    contract = result["orders"]
    assert [tool["name"] for tool in contract["tools"]] == ["read", "summarize"]
    read = contract["tools"][0]
    assert read["inputSchema"]["properties"]["region"]["default"] == "eu"
    assert read["outputSchema"]["properties"]["status"]["enum"] == ["open", "closed"]
    assert contract["tools"][1]["outputSchema"]["properties"]["total"]["type"] == (
        "integer"
    )
    assert contract["resources"] == [
        {
            "uri": "orders://latest",
            "outputSchema": {
                "type": "object",
                "properties": {"id": {"type": "string"}},
            },
        },
        {"uri": "ui://orders/app.html", "outputSchema": {}},
    ]
    assert list(contract["sources"]) == ["server.py"]
    [lookup] = result["low"]["tools"]
    assert list(lookup["outputSchema"]["properties"]) == ["sku", "status"]
    assert result["low"]["resources"] == []

    config = tmp_path / "verify.json"
    source = orders / "server.py"
    config.write_text(
        json.dumps({"sources": {str(source): contract["sources"]["server.py"]}})
    )
    runner = [sys.executable, str(tmp_path / "tooling/runner.py")]
    subprocess.run([*runner, "verify", str(config)], check=True)
    source.write_text(source.read_text() + "# changed\n")
    checked = subprocess.run(
        [*runner, "verify", str(config)], text=True, capture_output=True, check=False
    )
    assert checked.returncode != 0
    assert "sources changed after extraction" in checked.stderr


def test_an_entry_that_raises_while_importing_becomes_a_diagnostic(tmp_path):
    app = write(tmp_path / "app", {"server.py": "raise RuntimeError('no database')"})
    diagnostic = extract(tmp_path, {"broken": app})["broken"]["diagnostic"]
    assert diagnostic.startswith("server.py: Traceback")
    assert diagnostic.rstrip().endswith("RuntimeError: no database")
