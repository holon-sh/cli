"""The bundled extractor runs the app in a capability-stripped sandbox and asks its server."""

import json
import os
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

REGION = """
    from enum import Enum

    class Region(Enum):
        EU = "eu"
        US = "us"
"""

SERVER = """
    from mcp.server import MCPServer
    from catalog.region import Region
    from models import Order, Summary

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
    for name, source in files.items():
        (directory / name).parent.mkdir(parents=True, exist_ok=True)
        (directory / name).write_text(dedent(source))
    return directory


def extract(
    tmp_path: Path, apps: dict[str, Path], entry: str = "server.py", **env: str
) -> dict[str, dict]:
    archive = Path(
        subprocess.check_output(
            [sys.executable, "-I", str(ROOT / "build.py")], text=True
        ).strip()
    )
    with zipfile.ZipFile(archive) as bundle:
        bundle.extractall(tmp_path / "tooling")
    config = tmp_path / "config.json"
    output = tmp_path / "output.json"
    entries = [{"name": n, "path": str(p), "entry": entry} for n, p in apps.items()]
    config.write_text(json.dumps({"apps": entries}))
    extractor = [sys.executable, "-I", str(tmp_path / "tooling/extract.py")]
    environment = {**os.environ, **env}
    subprocess.run([*extractor, str(config), str(output)], check=True, env=environment)
    return json.loads(output.read_text())


def test_bundle_extracts_the_servers_own_catalog_and_detects_changed_inputs(tmp_path):
    files = {"server.py": SERVER, "models.py": MODELS, "catalog/region.py": REGION}
    orders = write(tmp_path / "orders", {**files, "catalog/__init__.py": ""})
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
    # Every application file the loader served is a recorded input.
    assert list(contract["sources"]) == [
        "catalog/__init__.py",
        "catalog/region.py",
        "models.py",
        "server.py",
    ]
    [lookup] = result["low"]["tools"]
    assert list(lookup["outputSchema"]["properties"]) == ["sku", "status"]
    assert result["low"]["resources"] == []

    config = tmp_path / "verify.json"
    source = orders / "models.py"
    recorded = {str(orders / name): d for name, d in contract["sources"].items()}
    config.write_text(json.dumps({"sources": recorded}))
    runner = [sys.executable, str(tmp_path / "tooling/runner.py")]
    subprocess.run([*runner, "verify", str(config)], check=True)
    (orders / "build.py").write_text("# a build output is not an input\n")
    subprocess.run([*runner, "verify", str(config)], check=True)
    source.write_text(source.read_text() + "# changed\n")
    checked = subprocess.run(
        [*runner, "verify", str(config)], text=True, capture_output=True, check=False
    )
    assert checked.returncode != 0
    assert "sources changed after extraction" in checked.stderr


def test_a_src_entry_imports_from_the_application_root(tmp_path):
    server = SERVER.replace("from models", "from src.models")
    files = {
        "src/models.py": MODELS,
        "src/server.py": server,
        "catalog/region.py": REGION,
    }
    app = write(tmp_path / "app", files)
    result = extract(tmp_path, {"orders": app}, entry="src/server.py")["orders"]
    assert [tool["name"] for tool in result["tools"]] == ["read", "summarize"]


EFFECTS = {
    "env": (
        "import os\n\nKEY = os.environ['API_KEY']\n",
        3,
        "os.environ",
        "environment",
    ),
    "open": (
        "import json\n\nSCHEMA = json.load(open('schema.json'))\n",
        3,
        "open",
        "filesystem",
    ),
    "socket": (
        "import socket\n\nsocket.create_connection(('example.com', 443))\n",
        3,
        "socket.create_connection",
        "network",
    ),
    "clock": (
        "from datetime import datetime\n\nSTARTED = datetime.now()\n",
        3,
        "datetime.now",
        "clock",
    ),
}


def test_module_scope_effects_fail_with_the_capability_file_and_line(tmp_path):
    apps = {
        name: write(tmp_path / name, {"server.py": source})
        for name, (source, *_) in EFFECTS.items()
    }
    apps["sqlite"] = write(tmp_path / "sqlite", {"server.py": "import sqlite3\n"})
    # Naming a guarded class is not using it, as eagerly evaluated annotations do.
    typed = "import subprocess\nfrom mcp.server import MCPServer\n"
    typed += "Runner = subprocess.Popen[bytes]\nserver = MCPServer('typed')\n"
    apps["typed"] = write(tmp_path / "typed", {"server.py": typed})
    apps["missing"] = write(tmp_path / "missing", {"server.py": "import requests\n"})
    result = extract(tmp_path, apps)
    for name, (_, line, api, capability) in EFFECTS.items():
        diagnostic = result[name]["diagnostic"]
        assert f'{name}/server.py", line {line}, in <module>' in diagnostic, diagnostic
        assert diagnostic.rstrip().endswith(
            f"Unavailable: {api} is unavailable: contract evaluation has no {capability}"
        ), diagnostic
    assert (
        result["sqlite"]["diagnostic"]
        .rstrip()
        .endswith(
            "ImportError: sqlite3 is unavailable: contract evaluation allows application "
            "code only pure standard-library modules"
        )
    )
    assert (
        result["missing"]["diagnostic"]
        .rstrip()
        .endswith(
            "MissingModule: No module named 'requests'; install the application's "
            "dependencies or commit holon-contract.json"
        )
    )
    assert result["missing"]["missing"] == "requests"
    assert result["typed"]["tools"] == []


def test_an_entry_that_raises_or_hangs_while_importing_becomes_a_diagnostic(tmp_path):
    broken = write(
        tmp_path / "broken", {"server.py": "raise RuntimeError('no database')"}
    )
    diagnostic = extract(tmp_path, {"broken": broken})["broken"]["diagnostic"]
    assert diagnostic.startswith("server.py: Traceback")
    assert diagnostic.rstrip().endswith("RuntimeError: no database")
    hung = write(tmp_path / "hung", {"server.py": "while True:\n    pass\n"})
    result = extract(tmp_path, {"hung": hung}, HOLON_CONTRACT_TIMEOUT="1")
    assert result["hung"]["diagnostic"] == (
        "server.py: no catalog within 1s; raise HOLON_CONTRACT_TIMEOUT if "
        "module-scope work needs longer"
    )
