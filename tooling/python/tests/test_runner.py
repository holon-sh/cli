"""The bundled extractor needs no site-packages; the runner detects changed sources."""

import hashlib
import json
import subprocess
import sys
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def test_bundle_extracts_without_site_packages_and_detects_changed_sources(tmp_path):
    archive = Path(
        subprocess.check_output(
            [sys.executable, "-I", str(ROOT / "build.py")], text=True
        ).strip()
    )
    assert archive.parent == ROOT.parents[1] / "pkg/tooling"
    with zipfile.ZipFile(archive) as bundle:
        assert set(bundle.namelist()) == {"runner.py", "extract.py"}
        bundle.extractall(tmp_path / "tooling")
    app = tmp_path / "app"
    app.mkdir()
    source = app / "server.py"
    source.write_text("""from holon.apps import apps
from mcp.server import MCPServer
server = MCPServer("orders")
@server.tool()
def read(id: str) -> str:
    raise RuntimeError("must not execute")
""")
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
    extractor = [sys.executable, "-S", str(tmp_path / "tooling/extract.py")]
    subprocess.run([*extractor, str(config), str(output)], check=True)
    runner = [sys.executable, str(tmp_path / "tooling/runner.py")]
    result = json.loads(output.read_text())["orders"]
    assert [tool["name"] for tool in result["tools"]] == ["read"]
    assert result["sources"] == {
        "server.py": hashlib.sha256(source.read_bytes()).hexdigest()
    }
    config.write_text(
        json.dumps(
            {
                "sources": {str(source): result["sources"]["server.py"]},
                "check": True,
                "entry": str(source),
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
