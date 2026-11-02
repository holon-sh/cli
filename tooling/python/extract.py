"""Obtain an application's MCP contract by running its server and asking it.

Usage: extract.py contract ROOT ENTRY. The contract (tools, resources and the application files the
server loaded) is printed on stdout; a failure is a diagnostic on stderr with status 2, or status 3
with the uninstalled package's name as the last line. The runner re-runs itself without site
packages or the caller's environment, where an import finder serves the application's files, the
standard library and its environment's installed packages (mcp and pydantic among them), and names
any module it cannot serve. The server sees the same world on every machine: an empty
environment, a fixed clock and seeded randomness, so its contract is the same everywhere. Reading
files outside the application, its packages and the standard library, writing files, sockets and
child processes raise naming the capability. The interpreter is the application's environment when
that has mcp, else the tooling's; the CLI chooses.
"""

from __future__ import annotations

import asyncio
import builtins
import datetime
import hashlib
import importlib.abc
import importlib.machinery
import importlib.util
import io
import json
import os
import random
import socket
import subprocess
import sys
import sysconfig
import time
import traceback
from collections.abc import Callable, Sequence
from pathlib import Path
from types import ModuleType
from typing import TYPE_CHECKING, Any, NoReturn, Self, TextIO

if TYPE_CHECKING:
    from mcp.server import MCPServer, Server

RESOURCE_SCHEMA = "sh.holon/outputSchema"
INSTALLED = {"site-packages", "dist-packages", ".venv", "venv", "node_modules"}
STDLIB = Path(sysconfig.get_paths()["stdlib"]).resolve()
EPOCH = 315532800  # 1980-01-01T00:00:00Z, the instant the frozen clock shows
MONOTONIC = time.monotonic


class Unavailable(RuntimeError):
    """A capability that contract evaluation does not have."""


class MissingModule(ModuleNotFoundError):
    """A module that neither the application nor its installed packages provide."""


def unavailable(name: str, capability: str) -> Callable[..., NoReturn]:
    def fail(*args: object, **kwargs: object) -> NoReturn:
        raise Unavailable(
            f"{name} is unavailable: contract evaluation has no {capability}"
        )

    return fail


class Finder(importlib.abc.MetaPathFinder):
    """Serves the application's files and installed packages, recording application files."""

    def __init__(self, root: Path, entry: Path) -> None:
        self.root = root
        self.packages = installed()
        self.search = list(
            dict.fromkeys([str(root), str(entry.parent), *self.packages])
        )
        self.served: set[Path] = set()
        # importlib.metadata finds distributions on sys.path; imports still go through find_spec.
        sys.path.extend(
            directory for directory in self.packages if directory not in sys.path
        )

    def application(self, path: Path) -> bool:
        if not path.is_relative_to(self.root):
            return False
        return not INSTALLED & set(path.relative_to(self.root).parts)

    def find_spec(
        self, name: str, path: Sequence[str] | None, target: ModuleType | None = None
    ) -> importlib.machinery.ModuleSpec | None:
        if path is None and name in sys.stdlib_module_names:
            return None
        search = self.search if path is None else path
        spec = importlib.machinery.PathFinder.find_spec(name, search, target)
        if spec is None and name == "holon.apps":
            return importlib.machinery.ModuleSpec(name, EmptyApps())
        if spec is None and path is None:
            raise MissingModule(
                f"No module named {name!r}; install the application's dependencies "
                "or commit holon-contract.json",
                name=name,
            )
        if (
            spec is not None
            and spec.origin
            and self.application(Path(spec.origin).resolve())
        ):
            self.served.add(Path(spec.origin).resolve())
        return spec


class EmptyApps(importlib.abc.Loader):
    def exec_module(self, module: ModuleType) -> None:
        apps = importlib.import_module("holon.client").Apps
        vars(module).update(
            apps=apps({"requires": {}}, {}),
            create_apps=lambda bindings: apps({"requires": {}}, {}, bindings),
        )


def installed() -> list[str]:
    """The environment's package directories, which -S leaves off sys.path."""
    # Without site, a virtual environment's prefix is not applied; it is the interpreter's parent.
    venv = str(Path(sys.executable).parent.parent)
    if Path(venv, "pyvenv.cfg").is_file():
        paths = sysconfig.get_paths("venv", vars={"base": venv, "platbase": venv})
    else:
        paths = sysconfig.get_paths()
    found = list(dict.fromkeys([paths["purelib"], paths["platlib"]]))
    for directory in list(found):
        for pth in sorted(Path(directory).glob("*.pth")):
            for line in pth.read_text().splitlines():
                entry = Path(directory, line.strip())
                if (
                    line.strip()
                    and not line.startswith(("#", "import"))
                    and entry.is_dir()
                ):
                    found.append(str(entry.resolve()))
    return found


class FrozenDatetime(datetime.datetime):
    @classmethod
    def now(cls, tz: datetime.tzinfo | None = None) -> Self:
        instant = cls.fromtimestamp(EPOCH, tz or datetime.UTC)
        return instant if tz else instant.replace(tzinfo=None)

    @classmethod
    def utcnow(cls) -> Self:
        return cls.now()

    @classmethod
    def today(cls) -> Self:
        return cls.now()


class FrozenDate(datetime.date):
    @classmethod
    def today(cls) -> Self:
        return cls(1980, 1, 1)


def freeze(finder: Finder) -> None:
    """Give the server the same world on every machine, for every caller, libraries included."""
    os.environ.clear()
    random.seed(0)
    frozen: dict[tuple[object, str], object] = {
        (os, "urandom"): lambda size: bytes(size),
        (time, "time"): lambda: float(EPOCH),
        (time, "time_ns"): lambda: EPOCH * 10**9,
        (time, "monotonic"): lambda: float(EPOCH),
        (datetime, "datetime"): FrozenDatetime,
        (datetime, "date"): FrozenDate,
    }
    for (owner, name), value in frozen.items():
        if isinstance(value, type):
            value.__name__ = value.__qualname__ = name
        setattr(owner, name, value)
    forbid(socket, "socket socketpair create_connection getaddrinfo", "network")
    forbid(subprocess, "Popen run call check_call check_output", "child processes")
    forbid(
        os,
        "system popen execl execle execlp execlpe execv execve execvp execvpe",
        "child processes",
    )
    readable = [finder.root, STDLIB, *(Path(p).resolve() for p in finder.packages)]
    real_open, real_os_open = builtins.open, os.open

    def check(name: str, file: Any, writing: bool) -> None:
        path = Path(os.fsdecode(file)).resolve()
        if writing:
            unavailable(f"{name} {path} for writing", "file writes")()
        if not any(path.is_relative_to(directory) for directory in readable):
            unavailable(
                f"{name} {path}",
                "files outside the application, its packages and the standard library",
            )()
        if finder.application(path):
            finder.served.add(path)

    def frozen_open(file: Any, mode: str = "r", *args: Any, **kwargs: Any) -> Any:
        if not isinstance(file, int):
            check("open", file, any(flag in mode for flag in "wax+"))
        return real_open(file, mode, *args, **kwargs)

    def frozen_os_open(path: Any, flags: int, *args: Any, **kwargs: Any) -> int:
        writes = os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_APPEND | os.O_TRUNC
        check("os.open", path, bool(flags & writes))
        return real_os_open(path, flags, *args, **kwargs)

    builtins.open = io.open = frozen_open
    os.open = frozen_os_open


def forbid(owner: ModuleType, names: str, capability: str) -> None:
    for name in names.split():
        real = getattr(owner, name, None)
        fail = unavailable(f"{owner.__name__}.{name}", capability)
        if isinstance(real, type):
            # A class stays a class, so annotations such as subprocess.Popen[bytes] still evaluate.
            fail = type(
                real.__name__,
                (real,),
                {"__init__": fail, "__module__": real.__module__},
            )
        if real is not None:
            setattr(owner, name, fail)


class Loop(asyncio.SelectorEventLoop):
    """The client's event loop keeps a running clock under the frozen one."""

    def time(self) -> float:
        return MONOTONIC()


def find_server(module: object) -> MCPServer | Server[Any]:
    from mcp.server import MCPServer, Server

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
    from mcp.client import Client

    tools: list[dict[str, Any]] = []
    resources: list[dict[str, Any]] = []
    async with Client(server) as client:
        info = client.server_info
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
        "serverInfo": info.model_dump(by_alias=True, exclude_none=True) if info else {},
        "tools": sorted(tools, key=lambda tool: tool["name"]),
        "resources": sorted(resources, key=lambda resource: resource["uri"]),
    }


def contract(root: Path, entry: str, stdout: TextIO) -> int:
    """Run one entry, ask its server for the catalog and print it; return the exit status."""
    os.chdir(root)  # relative paths name application files, wherever the runner started
    path = root / entry
    finder = Finder(root, path)
    sys.meta_path.insert(0, finder)
    loop = Loop()  # before freezing: its wakeup channel is a socket pair
    try:
        freeze(finder)
        # Import the entry as a script beside its project would, without its __main__ block.
        spec = importlib.util.spec_from_file_location(path.stem, path)
        if spec is None or spec.loader is None:
            raise ImportError(f"cannot import {path}")
        finder.served.add(path.resolve())
        module = sys.modules[path.stem] = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        result = loop.run_until_complete(catalog(find_server(module)))
    except (Exception, SystemExit) as error:  # noqa: BLE001 - application code can raise anything
        frames = [
            frame
            for frame in traceback.extract_tb(error.__traceback__)
            if frame.filename != __file__ and not frame.filename.startswith("<frozen")
        ]
        trace = traceback.format_list(frames)
        if isinstance(error, MissingModule):
            trace.append(f"{type(error).__name__}: {error}\n")
        else:
            trace += traceback.format_exception_only(error)
        sys.stderr.write(
            f"{entry}: Traceback (most recent call last):\n{''.join(trace)}"
        )
        if isinstance(error, MissingModule) and error.name:
            sys.stderr.write(error.name.partition(".")[0] + "\n")
            return 3
        return 2
    sources = {}
    for file in sorted(finder.served):
        with file.open("rb") as source:
            digest = hashlib.file_digest(source, "sha256").hexdigest()
        sources[file.relative_to(root).as_posix()] = digest
    json.dump({**result, "sources": sources}, stdout)
    stdout.flush()  # Python 3.11 loses it at exit once the server's world is frozen
    return 0


if __name__ == "__main__":
    mode, root, entry = sys.argv[1:]
    if mode != "contract":
        sys.exit(f"unknown operation {mode}")
    if not sys.flags.no_site:
        # Without site packages, the caller's environment or bytecode beside the sources, and with
        # the same string hashes, so set order does not vary between runs.
        argv = [sys.executable, "-s", "-S", "-P", "-B", *sys.argv]
        os.execve(sys.executable, argv, {"PYTHONHASHSEED": "0"})
    # The contract is all that stdout carries; whatever the application prints goes to stderr.
    stdout = os.fdopen(os.dup(1), "w")
    os.dup2(2, 1)
    sys.exit(contract(Path(root).resolve(), entry, stdout))
