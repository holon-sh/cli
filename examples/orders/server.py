from mcp.server import MCPServer
from mcp.server.mcpserver.exceptions import ToolError
from pydantic import BaseModel


class Order(BaseModel):
    id: str
    total: float


server = MCPServer("orders")

ORDERS = {"A-1": Order(id="A-1", total=42.0), "A-2": Order(id="A-2", total=17.5)}


@server.tool()
def lookup(id: str) -> Order:
    """Look up an order by id."""
    if id not in ORDERS:
        raise ToolError(f"unknown order {id}")
    return ORDERS[id]


if __name__ == "__main__":
    server.run("streamable-http", port=3001)
