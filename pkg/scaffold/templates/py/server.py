from pathlib import Path

from mcp.server import MCPServer
from mcp.server.apps import APP_MIME_TYPE

here = Path(__file__).parent
# Holon serves the page built into dist/web at /{{.App}}/.
frontend = here / "dist/web"
view = "ui://{{.App}}/view.html"

server = MCPServer("{{.Name}}", version="0.1.0", description="The {{.App}} app")


@server.tool(meta={"ui": {"resourceUri": view}})
def greet(name: str) -> str:
    """Greet someone by name."""
    return f"Hello, {name}!"


@server.resource(view, name="View", mime_type=APP_MIME_TYPE)
def read_view() -> str:
    return (here / "dist/mcp/index.html").read_text()
