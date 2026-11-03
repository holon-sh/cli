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

# A src entry imports from the application root, and reads files there.
SERVER = """
    import json

    from mcp.server import MCPServer
    from catalog.region import Region
    from holon.apps import apps
    from src.models import Order

    server = MCPServer("orders")

    @server.tool()
    def read(id: str, region: Region = Region.EU) -> Order:
        return apps.inventory.tools.read()

    ORDER = json.load(open("order.json"))

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

# With the runtime installed, the generated module's real clients stand in.
INSTALLED = """
    from mcp.server import MCPServer
    from holon.apps import apps

    server = MCPServer("installed")

    @server.tool()
    def ready(ready: str = str(apps.ready)) -> str:
        raise RuntimeError("must not be called")
"""


def write(directory: Path, files: dict[str, str]) -> Path:
    for name, source in files.items():
        (directory / name).parent.mkdir(parents=True, exist_ok=True)
        (directory / name).write_text(dedent(source))
    return directory


def extract(tmp_path: Path, apps: dict[str, tuple[Path, str]]) -> dict[str, dict]:
    """Each app's contract, or its diagnostic and the package it missed."""
    archive = Path(
        subprocess.check_output(
            [sys.executable, "-I", str(ROOT / "build.py")], text=True
        ).strip()
    )
    with zipfile.ZipFile(archive) as bundle:
        bundle.extractall(tmp_path / "tooling")
    extractor = [sys.executable, str(tmp_path / "tooling/extract.py"), "contract"]
    results = {}
    for name, (root, entry) in apps.items():
        run = subprocess.run(
            [*extractor, str(root), entry], capture_output=True, text=True, check=False
        )
        assert run.returncode in (0, 2, 3), run.stderr
        if run.returncode == 0:
            results[name] = json.loads(run.stdout)
        else:
            missing = run.stderr.splitlines()[-1] if run.returncode == 3 else None
            results[name] = {"diagnostic": run.stderr, "missing": missing}
    return results


def test_the_bundle_extracts_each_servers_catalog_and_detects_changed_sources(
    tmp_path,
):
    files = {"src/server.py": SERVER, "src/models.py": MODELS}
    files |= {"catalog/__init__.py": "", "catalog/region.py": REGION}
    files["order.json"] = '{"type": "object", "properties": {"id": {"type": "string"}}}'
    orders = write(tmp_path / "orders", files)
    low = write(tmp_path / "low", {"server.py": LOW_LEVEL})
    installed = write(
        tmp_path / "installed",
        {
            "server.py": INSTALLED,
            "holon/client.py": "class Apps:\n    ready = True\n    def __init__(self, *args):\n        pass\n",
        },
    )
    result = extract(
        tmp_path,
        {
            "orders": (orders, "src/server.py"),
            "low": (low, "server.py"),
            "installed": (installed, "server.py"),
        },
    )

    contract = result["orders"]
    assert [tool["name"] for tool in contract["tools"]] == ["read"]
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
    # Every application file the server imported or read is a recorded input.
    assert list(contract["sources"]) == [
        "catalog/__init__.py",
        "catalog/region.py",
        "order.json",
        "src/models.py",
        "src/server.py",
    ]
    assert [tool["name"] for tool in result["low"]["tools"]] == ["lookup"]
    assert [tool["name"] for tool in result["installed"]["tools"]] == ["ready"]
    assert list(result["installed"]["sources"]) == [
        "holon/client.py",
        "server.py",
    ]

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


FROZEN = """
    import os
    import random
    import time
    from datetime import datetime

    from mcp.server import MCPServer

    SEEN = f"{os.environ.get('API_KEY')} {time.time()} {datetime.now()} {random.random()}"
    server = MCPServer("frozen")

    @server.tool()
    def read(seen: str = SEEN) -> str:
        raise RuntimeError("must not be called")
"""


def test_module_scope_environment_clock_and_randomness_give_one_contract(
    tmp_path, monkeypatch
):
    app = write(tmp_path / "frozen", {"server.py": FROZEN})
    contracts = []
    for key in ("first", "second"):
        monkeypatch.setenv("API_KEY", key)
        contracts.append(extract(tmp_path, {"frozen": (app, "server.py")})["frozen"])
    assert [tool["name"] for tool in contracts[0]["tools"]] == ["read"]
    assert contracts[0] == contracts[1]


def test_effects_and_missing_packages_fail_naming_them(tmp_path):
    sources = {
        "socket": "import socket\n\nsocket.create_connection(('example.com', 443))\n",
        "write": "import json\n\njson.dump({}, open('cache.json', 'w'))\n",
        "missing": "import requests\n",
        "clients": "from holon.apps import apps\n\napps.inventory\n",
        # Naming a forbidden class is not using it, as eagerly evaluated annotations do.
        "typed": "import subprocess\nfrom mcp.server import MCPServer\n"
        "Runner = subprocess.Popen[bytes]\nserver = MCPServer('typed')\n",
    }
    apps = {
        name: (write(tmp_path / name, {"server.py": source}), "server.py")
        for name, source in sources.items()
    }
    # A missing import inside the installed clients names that package, not a stub.
    apps["vendored"] = (
        write(
            tmp_path / "vendored",
            {
                "server.py": "from holon.apps import apps\n",
                "holon/client.py": "import vendored\n",
            },
        ),
        "server.py",
    )
    result = extract(tmp_path, apps)
    for name, capability in (
        ("socket", "network"),
        ("write", "file writes"),
        ("clients", "generated clients"),
    ):
        diagnostic = result[name]["diagnostic"]
        assert f'{name}/server.py", line 3' in diagnostic, diagnostic
        assert f"contract evaluation has no {capability}" in diagnostic, diagnostic
    assert result["missing"]["missing"] == "requests"
    assert result["vendored"]["missing"] == "vendored"
    assert result["typed"]["tools"] == []
