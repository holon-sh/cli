"""The bundled extractor never runs app code; the runner detects changed sources."""

import json
import subprocess
import sys
import zipfile
from pathlib import Path
from textwrap import dedent

import pytest

ROOT = Path(__file__).resolve().parents[1]


@pytest.mark.parametrize("newline", ["\n", "\r\n"])
def test_bundle_extracts_without_running_sources_and_detects_changes(tmp_path, newline):
    archive = Path(
        subprocess.check_output(
            [sys.executable, "-I", str(ROOT / "build.py")], text=True
        ).strip()
    )
    with zipfile.ZipFile(archive) as bundle:
        bundle.extractall(tmp_path / "tooling")
    app = tmp_path / "app"
    app.mkdir()
    source = app / "server.py"
    source.write_text(
        dedent("""
            from holon.apps import apps
            from mcp.server import MCPServer
            server = MCPServer("orders")
            @server.tool()
            def read(id: str) -> str:
                raise RuntimeError("must not execute")
        """),
        newline=newline,
    )
    generated = app / "holon/apps/__init__.py"
    generated.parent.mkdir(parents=True)
    generated.write_text("not even valid Python")
    config = tmp_path / "config.json"
    output = tmp_path / "output.json"
    config.write_text(
        json.dumps(
            {"apps": [{"name": "orders", "path": str(app), "entry": "server.py"}]}
        )
    )
    extractor = [sys.executable, "-I", str(tmp_path / "tooling/extract.py")]
    subprocess.run([*extractor, str(config), str(output)], check=True)
    runner = [sys.executable, str(tmp_path / "tooling/runner.py")]
    result = json.loads(output.read_text())["orders"]
    assert [tool["name"] for tool in result["tools"]] == ["read"]
    assert set(result["sources"]) == {"server.py"}
    config.write_text(
        json.dumps(
            {
                "sources": {str(source): result["sources"]["server.py"]},
            }
        )
    )
    subprocess.run([*runner, "verify", str(config)], check=True)
    source.write_text(source.read_text() + "# changed\n")
    checked = subprocess.run(
        [*runner, "verify", str(config)], text=True, capture_output=True, check=False
    )
    assert checked.returncode != 0
    assert "sources changed after extraction" in checked.stderr
