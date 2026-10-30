from extract import extract


def test_app_retains_native_metadata_and_changes_without_importing(tmp_path):
    (tmp_path / "server.py").write_text('''
from holon.app import App
from mcp.types import ToolAnnotations
raise RuntimeError("Never import the application")
server = App("counter", version="1")
@server.resource("counter://state", mime_type="application/json")
def state() -> dict[str, int]:
    return {"count": 0}
@server.tool(changes=["counter://state"], annotations=ToolAnnotations(destructive_hint=False))
def add(amount: int) -> dict[str, int]:
    """Add to the counter."""
    raise RuntimeError("Never execute callbacks")
@server.tool(changes=[])
def show() -> dict[str, int]:
    raise RuntimeError("Never execute callbacks")
''')
    result = extract(tmp_path, "server.py")
    assert "diagnostic" not in result
    tool, show = result["tools"]
    assert tool["annotations"]["destructiveHint"] is False
    assert tool["_meta"]["sh.holon/changes"] == ["counter://state"]
    assert tool["description"] == "Add to the counter."
    assert "_meta" not in show
    assert result["resources"][0]["uri"] == "counter://state"
