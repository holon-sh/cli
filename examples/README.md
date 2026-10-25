# Examples

Two MCP servers and a browser view, used by the walkthrough in the [repository README](../README.md).

| Directory | Language | Role |
|---|---|---|
| [`orders/`](orders) | Python | Provider. An ordinary MCP server with one tool, `lookup`. |
| [`checkout/`](checkout) | TypeScript | Caller. Requires `orders` and calls `lookup` from its own `total` tool. Serves the view below as an MCP App resource. |
| [`checkout/ui/`](checkout/ui) | TypeScript (browser) | View. Calls `checkout`'s own `total` tool through the MCP Apps host, typed by `holon-ui`. |

```sh
cd examples/checkout
npm install && (cd ../orders && uv sync)
holon types generate . ../orders        # typed clients for checkout (node + browser) and orders
holon types check . ../orders           # tsc and mypy against the contracts
npm run build                           # bundles ui/ into dist/index.html
(cd ../orders && uv run python server.py) &
ORDERS_MCP_URL=http://127.0.0.1:3001/mcp npm run serve
```

`checkout` listens on `http://127.0.0.1:3002/mcp`; call its `total` tool with any MCP client, or open the `ui://checkout/index.html` resource in an MCP Apps host.
