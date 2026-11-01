"""The bundled extractor runs the app in a capability-stripped sandbox and asks its server."""

import json
import subprocess
import sys
import zipfile
from pathlib import Path
from textwrap import dedent

ROOT = Path(__file__).resolve().parents[1]

MODELS = """
    from pydantic import BaseModel

    class Order(BaseModel):
        id: str
"""

REGION = """
    from enum import Enum

    class Region(Enum):
        EU = "eu"
        US = "us"
"""

# A src entry imports from the application root.
SERVER = """
    from mcp.server import MCPServer
    from catalog.region import Region
    from src.models import Order

    server = MCPServer("orders")

    @server.tool()
    def read(id: str, region: Region = Region.EU) -> Order:
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

    async def tools(context, params):
        tool = types.Tool(name="lookup", input_schema={"type": "object"})
        return types.ListToolsResult(tools=[tool])

    app = Server("low", on_list_tools=tools)
"""


def write(directory: Path, files: dict[str, str]) -> Path:
    for name, source in files.items():
        (directory / name).parent.mkdir(parents=True, exist_ok=True)
        (directory / name).write_text(dedent(source))
    return directory


def extract(tmp_path: Path, apps: list[dict[str, str]]) -> dict[str, dict]:
    archive = Path(
        subprocess.check_output(
            [sys.executable, "-I", str(ROOT / "build.py")], text=True
        ).strip()
    )
    with zipfile.ZipFile(archive) as bundle:
        bundle.extractall(tmp_path / "tooling")
    config = tmp_path / "config.json"
    output = tmp_path / "output.json"
    config.write_text(json.dumps({"apps": apps}))
    extractor = [sys.executable, "-I", str(tmp_path / "tooling/extract.py")]
    subprocess.run([*extractor, str(config), str(output)], check=True)
    return json.loads(output.read_text())


def test_the_bundle_extracts_each_servers_catalog_and_detects_changed_sources(
    tmp_path,
):
    files = {"src/server.py": SERVER, "src/models.py": MODELS}
    files |= {"catalog/__init__.py": "", "catalog/region.py": REGION}
    orders = write(tmp_path / "orders", files)
    low = write(tmp_path / "low", {"server.py": LOW_LEVEL})
    # The app's own directory is used where visible (a host), else its snapshot (a container).
    absent = str(tmp_path / "absent")
    entry = "src/server.py"
    result = extract(
        tmp_path,
        [
            {"name": "orders", "path": absent, "source": str(orders), "entry": entry},
            {"name": "snapshot", "path": str(orders), "source": absent, "entry": entry},
            {"name": "low", "path": str(low), "entry": "server.py"},
        ],
    )

    contract = result["orders"]
    assert [tool["name"] for tool in contract["tools"]] == ["read"]
    assert result["snapshot"]["tools"] == contract["tools"]
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
    # Every application file the loader served is a recorded input.
    assert list(contract["sources"]) == [
        "catalog/__init__.py",
        "catalog/region.py",
        "src/models.py",
        "src/server.py",
    ]
    assert [tool["name"] for tool in result["low"]["tools"]] == ["lookup"]

    config = tmp_path / "verify.json"
    recorded = {str(orders / name): d for name, d in contract["sources"].items()}
    config.write_text(json.dumps({"sources": recorded}))
    runner = [sys.executable, str(tmp_path / "tooling/runner.py")]
    subprocess.run([*runner, "verify", str(config)], check=True)
    models = orders / "src/models.py"
    models.write_text(models.read_text() + "# changed\n")
    checked = subprocess.run(
        [*runner, "verify", str(config)], text=True, capture_output=True, check=False
    )
    assert checked.returncode != 0
    assert "sources changed after extraction" in checked.stderr


EFFECTS = {
    "env": ("import os\n\nKEY = os.environ['API_KEY']\n", "environment"),
    "open": ("import json\n\nSCHEMA = json.load(open('schema.json'))\n", "filesystem"),
    "socket": (
        "import socket\n\nsocket.create_connection(('example.com', 443))\n",
        "network",
    ),
}


def test_module_scope_effects_fail_naming_the_capability_and_line(tmp_path):
    sources = {name: source for name, (source, _) in EFFECTS.items()}
    # Other standard-library modules are unavailable; an uninstalled package is named.
    sources |= {"sqlite": "import sqlite3\n", "missing": "import requests\n"}
    apps = [
        {"name": name, "path": str(tmp_path / name), "entry": "server.py"}
        for name in sources
    ]
    for name, source in sources.items():
        write(tmp_path / name, {"server.py": source})
    result = extract(tmp_path, apps)
    for name, (_, capability) in EFFECTS.items():
        diagnostic = result[name]["diagnostic"]
        assert f'{name}/server.py", line 3' in diagnostic, diagnostic
        assert f"contract evaluation has no {capability}" in diagnostic, diagnostic
    assert "sqlite3 is unavailable" in result["sqlite"]["diagnostic"]
    assert result["missing"]["missing"] == "requests"
