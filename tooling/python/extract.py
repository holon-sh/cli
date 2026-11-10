"""Serve an application's MCP server over stdio, so the CLI can ask it for its contract.

Usage: extract.py serve ROOT ENTRY. The server answers the MCP messages on stdin, writing its
answers to stdout, and exits once it has answered every request; a failure is a diagnostic on
stderr with status 2, or status 3 with the uninstalled package's name as the last line. The runner
re-runs itself without site packages or the caller's environment, where an import finder serves the
application's files, the standard library and its environment's installed packages (mcp and pydantic
among them), and names
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
import importlib.abc
import importlib.machinery
import importlib.util
import io
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
    """Serves the application's files and installed packages."""

    def __init__(self, root: Path, entry: Path) -> None:
        self.root = root
        self.packages = installed()
        self.search = list(
            dict.fromkeys([str(root), str(entry.parent), *self.packages])
        )
        # importlib.metadata finds distributions on sys.path; imports still go through find_spec.
        sys.path.extend(
            directory for directory in self.packages if directory not in sys.path
        )

    def find_spec(
        self, name: str, path: Sequence[str] | None, target: ModuleType | None = None
    ) -> importlib.machinery.ModuleSpec | None:
        if path is None and name in sys.stdlib_module_names:
            return None
        search = self.search if path is None else path
        spec = importlib.machinery.PathFinder.find_spec(name, search, target)
        if name == "holon.apps" and (spec is None or spec.origin is None):
            return importlib.machinery.ModuleSpec(name, EmptyApps())
        if spec is None and path is None:
            raise MissingModule(
                f"No module named {name!r}; install the application's dependencies "
                "or commit holon-contract.json",
                name=name,
            )
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
    """The event loop keeps a running clock under the frozen one."""

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


async def serve(server: MCPServer | Server[Any], batch: str, stdout: TextIO) -> None:
    """Answer each JSON-RPC message in batch, a line each on stdout, and return once every
    request has its answer."""
    import anyio
    import mcp_types as types
    from mcp.server import MCPServer
    from mcp.server.lowlevel.server import lifespan
    from mcp.shared.memory import create_client_server_memory_streams
    from mcp.shared.message import SessionMessage

    # A lifespan is the server's startup, which serving a connection enters
    # (https://github.com/modelcontextprotocol/python-sdk/blob/v2.3.0/src/mcp/server/lowlevel/server.py#L722).
    # Listing the catalog does not start the server, so the SDK's no-op lifespan stands in
    # (https://github.com/modelcontextprotocol/python-sdk/blob/v2.3.0/src/mcp/server/lowlevel/server.py#L115-L117).
    lowlevel = server._lowlevel_server if isinstance(server, MCPServer) else server
    lowlevel.lifespan = lifespan
    messages = [
        types.jsonrpc_message_adapter.validate_json(line, by_name=False)
        for line in batch.splitlines()
        if line.strip()
    ]
    # The server cancels requests in flight when its input ends, so it ends once all are answered.
    pending = {m.id for m in messages if isinstance(m, types.JSONRPCRequest)}
    streams = create_client_server_memory_streams()
    async with streams as ((answers, requests), (server_read, server_write)):

        async def send() -> None:
            for message in messages:
                await requests.send(SessionMessage(message))
            if not pending:
                await requests.aclose()

        async def write() -> None:
            async for answer in answers:
                if isinstance(answer, Exception):
                    raise answer
                message = answer.message
                stdout.write(
                    message.model_dump_json(by_alias=True, exclude_unset=True) + "\n"
                )
                stdout.flush()
                if isinstance(message, (types.JSONRPCResponse, types.JSONRPCError)):
                    pending.discard(message.id)
                    if not pending:
                        await requests.aclose()

        async with anyio.create_task_group() as group:
            group.start_soon(send)
            group.start_soon(write)
            await lowlevel.run(
                server_read, server_write, lowlevel.create_initialization_options()
            )
            group.cancel_scope.cancel()


def serve_entry(root: Path, entry: str, batch: str, stdout: TextIO) -> int:
    """Run one entry and answer batch with its server on stdout; return the exit status."""
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
        module = sys.modules[path.stem] = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        loop.run_until_complete(serve(find_server(module), batch, stdout))
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
    return 0


if __name__ == "__main__":
    mode, root, entry = sys.argv[1:]
    if mode != "serve":
        sys.exit(f"unknown operation {mode}")
    if not sys.flags.no_site:
        # Without site packages, the caller's environment or bytecode beside the sources, and with
        # the same string hashes, so set order does not vary between runs.
        argv = [sys.executable, "-s", "-S", "-P", "-B", *sys.argv]
        os.execve(sys.executable, argv, {"PYTHONHASHSEED": "0"})
    # MCP is all that stdin and stdout carry; the application reads nothing, and what it prints goes to
    # stderr.
    batch, stdout = sys.stdin.read(), os.fdopen(os.dup(1), "w")
    os.dup2(os.open(os.devnull, os.O_RDONLY), 0)
    os.dup2(2, 1)
    sys.exit(serve_entry(Path(root).resolve(), entry, batch, stdout))
