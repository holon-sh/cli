"""Obtain an application's MCP contract by running its server and asking it.

Usage: extract.py CONFIG OUTPUT. Each entry is imported in its own child process, with the
application root (then the entry's directory) first on sys.path and HOLON_CONTRACT=1 set, so
applications can skip start-up side effects. A child that has not answered within
HOLON_CONTRACT_TIMEOUT seconds (30 by default) is killed. The application's own mcp and pydantic
compute the schemas, so they must be importable by the interpreter running this file (the CLI
prefers the application's .venv).
"""

from __future__ import annotations

import asyncio
import hashlib
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import traceback
from pathlib import Path
from typing import Any

from mcp.client import Client
from mcp.server import MCPServer, Server

RESOURCE_SCHEMA = "sh.holon/outputSchema"


def find_server(module: object) -> MCPServer | Server[Any]:
    servers = {
        name: value
        for name, value in vars(module).items()
        if isinstance(value, (MCPServer, Server))
    }
    if "server" in servers:
        return servers["server"]
    if len(servers) == 1:
        return next(iter(servers.values()))
    found = ", ".join(sorted(servers)) or "none"
    raise LookupError(
        f"expose one MCPServer or Server as the module attribute server (found {found})"
    )


async def catalog(server: MCPServer | Server[Any]) -> dict[str, Any]:
    tools: list[dict[str, Any]] = []
    resources: list[dict[str, Any]] = []
    async with Client(server) as client:
        capabilities = client.server_capabilities
        cursor = None
        while capabilities and capabilities.tools:
            page = await client.list_tools(cursor=cursor)
            for tool in page.tools:
                schemas = {"inputSchema": tool.input_schema}
                if tool.output_schema is not None:
                    schemas["outputSchema"] = tool.output_schema
                tools.append({"name": tool.name, **schemas})
            if not (cursor := page.next_cursor):
                break
        while capabilities and capabilities.resources:
            listed = await client.list_resources(cursor=cursor)
            # Like a live server's contract, a resource is typed only by its sh.holon/outputSchema.
            resources += [
                {
                    "uri": str(r.uri),
                    "outputSchema": (r.meta or {}).get(RESOURCE_SCHEMA, {}),
                }
                for r in listed.resources
            ]
            if not (cursor := listed.next_cursor):
                break
    return {
        "tools": sorted(tools, key=lambda tool: tool["name"]),
        "resources": sorted(resources, key=lambda resource: resource["uri"]),
    }


def contract(root: Path, entry: str) -> dict[str, Any]:
    """Import one entry, in the child process, and ask its server for the catalog."""
    path = root / entry
    # Import the entry as a script beside its project would, without its __main__ block.
    sys.path[:0] = dict.fromkeys([str(root), str(path.parent)])
    try:
        spec = importlib.util.spec_from_file_location(path.stem, path)
        if spec is None or spec.loader is None:
            raise ImportError(f"cannot import {path}")
        module = sys.modules[path.stem] = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        result = asyncio.run(catalog(find_server(module)))
    except (Exception, SystemExit) as error:  # noqa: BLE001 - application code can raise anything
        return {"diagnostic": f"{entry}: {''.join(traceback.format_exception(error))}"}
    # The whole snapshot is the input; the entry digest lets check refuse a rebuilt entry.
    with path.open("rb") as source:
        digest = hashlib.file_digest(source, "sha256").hexdigest()
    return {**result, "sources": {entry: digest}}


def extract(root: str | Path, entry: str) -> dict[str, Any]:
    timeout = float(os.environ.get("HOLON_CONTRACT_TIMEOUT") or 30)
    flags = ["-I"] if sys.flags.isolated else []
    with tempfile.TemporaryDirectory() as directory:
        output = Path(directory, "contract.json")
        command = [sys.executable, *flags, __file__, "--contract", str(root), entry]
        try:
            subprocess.run(
                [*command, str(output)],
                cwd=root,
                env={**os.environ, "HOLON_CONTRACT": "1"},
                timeout=timeout,
                check=False,
            )
        except subprocess.TimeoutExpired:
            return {
                "diagnostic": f"{entry}: no catalog within {timeout:g}s; gate start-up "
                "work on HOLON_CONTRACT or raise HOLON_CONTRACT_TIMEOUT"
            }
        if not output.exists():
            return {"diagnostic": f"{entry}: exited before answering"}
        result: dict[str, Any] = json.loads(output.read_text())
        return result


if __name__ == "__main__" and sys.argv[1] == "--contract":
    # extract.py --contract ROOT ENTRY OUTPUT: one application, in its own process.
    root, entry, output = sys.argv[2:]
    Path(output).write_text(json.dumps(contract(Path(root).resolve(), entry)))
elif __name__ == "__main__":
    # extract.py CONFIG OUTPUT: contracts for every app in the config, as JSON.
    config, output = sys.argv[1:]
    apps = json.loads(Path(config).read_text())["apps"]
    Path(output).write_text(
        json.dumps({app["name"]: extract(app["path"], app["entry"]) for app in apps})
    )
