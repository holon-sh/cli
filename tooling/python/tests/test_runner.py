"""The bundled extractor runs the app in a capability-stripped sandbox and asks its server."""

import json
import subprocess
import sys
import sysconfig
import tomllib
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

# A startup, which listing the catalog must not run.
LIFESPAN = """
    from contextlib import asynccontextmanager

    @asynccontextmanager
    async def lifespan(server):
        raise RuntimeError("startup ran")
        yield
"""

# A src entry imports from the application root, and reads files there.
SERVER = """
    import json

    from mcp.server import MCPServer
    from catalog.region import Region
    from src.lifespan import lifespan
    from src.models import Order

    server = MCPServer("orders", lifespan=lifespan)

    @server.tool()
    def read(id: str, region: Region = Region.EU) -> Order:
        raise RuntimeError("must not be called")

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
    from lifespan import lifespan

    async def tools(context, params):
        tool = types.Tool(name="lookup", input_schema={"type": "object"})
        return types.ListToolsResult(tools=[tool])

    app = Server("low", on_list_tools=tools, lifespan=lifespan)
"""


def write(directory: Path, files: dict[str, str]) -> Path:
    for name, source in files.items():
        (directory / name).parent.mkdir(parents=True, exist_ok=True)
        (directory / name).write_text(dedent(source))
    return directory


def extract(
    apps: dict[str, tuple[Path, str]], python: Path | str = sys.executable
) -> dict[str, dict]:
    """Each app's contract, or its diagnostic and the package it missed."""
    extractor = [python, str(ROOT / "extract.py"), "contract"]
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


def test_the_bundle_extracts_each_servers_catalog(tmp_path):
    files = {
        "src/server.py": SERVER,
        "src/models.py": MODELS,
        "src/lifespan.py": LIFESPAN,
    }
    files |= {"catalog/__init__.py": "", "catalog/region.py": REGION}
    files["order.json"] = '{"type": "object", "properties": {"id": {"type": "string"}}}'
    orders = write(tmp_path / "orders", files)
    low = write(tmp_path / "low", {"server.py": LOW_LEVEL, "lifespan.py": LIFESPAN})
    result = extract(
        {"orders": (orders, "src/server.py"), "low": (low, "server.py")},
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
    assert [tool["name"] for tool in result["low"]["tools"]] == ["lookup"]


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
        contracts.append(extract({"frozen": (app, "server.py")})["frozen"])
    assert [tool["name"] for tool in contracts[0]["tools"]] == ["read"]
    assert contracts[0] == contracts[1]


def test_effects_and_missing_packages_fail_naming_them(tmp_path):
    sources = {
        "socket": "import socket\n\nsocket.create_connection(('example.com', 443))\n",
        "write": "import json\n\njson.dump({}, open('cache.json', 'w'))\n",
        "missing": "import requests\n",
        # Naming a forbidden class is not using it, as eagerly evaluated annotations do.
        "typed": "import subprocess\nfrom mcp.server import MCPServer\n"
        "Runner = subprocess.Popen[bytes]\nserver = MCPServer('typed')\n",
    }
    apps = {
        name: (write(tmp_path / name, {"server.py": source}), "server.py")
        for name, source in sources.items()
    }
    result = extract(apps)
    for name, capability in (("socket", "network"), ("write", "file writes")):
        diagnostic = result[name]["diagnostic"]
        assert f'{name}/server.py", line 3' in diagnostic, diagnostic
        assert f"contract evaluation has no {capability}" in diagnostic, diagnostic
    assert result["missing"]["missing"] == "requests"
    assert result["typed"]["tools"] == []


# Stands in for holon-sh, which ships a holon.apps that raises until the project generates its own.
SDK = {
    "holon/client.py": """
        class Apps:
            def __init__(self, *args):
                pass
    """,
    "holon/apps/__init__.py": """
        raise ImportError("no generated holon.apps on sys.path: run `holon types generate`")
    """,
}

CALLER = """
    import holon.apps
    from holon.apps import apps
    from mcp.server import MCPServer

    server = MCPServer(getattr(holon.apps, "SOURCE", "empty"))
"""

GENERATED = """\
    # holon-generated-apps-v1 app=caller
    SOURCE = "generated"
    apps = None
"""


def environment(directory: Path, files: dict[str, str]) -> Path:
    """A virtual environment holding files, which also sees the packages these tests run with."""
    subprocess.run(
        [sys.executable, "-m", "venv", "--without-pip", directory], check=True
    )
    python = directory / "bin" / "python"
    purelib = subprocess.run(
        [python, "-c", "import sysconfig; print(sysconfig.get_paths()['purelib'])"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout.strip()
    write(Path(purelib), files | {"tests.pth": sysconfig.get_paths()["purelib"]})
    return python


def test_holon_apps_is_empty_until_the_project_generates_it(tmp_path):
    python = environment(tmp_path / "env", SDK)
    app = write(tmp_path / "caller", {"server.py": CALLER})
    result = extract({"caller": (app, "server.py")}, python)["caller"]
    assert result.get("serverInfo", {}).get("name") == "empty", result
    write(app, {"holon/apps/__init__.py": GENERATED})
    result = extract({"caller": (app, "server.py")}, python)["caller"]
    assert result.get("serverInfo", {}).get("name") == "generated", result


def test_requirements_list_the_project_dependencies():
    project = tomllib.loads((ROOT / "pyproject.toml").read_text())["project"]
    requirements = (ROOT / "requirements.txt").read_text().splitlines()
    assert requirements == project["dependencies"]
