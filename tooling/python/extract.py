"""Obtain an application's MCP contract by running its server in a capability-stripped sandbox.

Usage: extract.py CONFIG OUTPUT. Each entry runs in its own `python -I -S` child process, killed
after HOLON_CONTRACT_TIMEOUT seconds (30 by default). There an import finder serves the
application's files and its environment's installed packages (mcp and pydantic among them),
allows application code only pure standard-library modules, and names any module it cannot serve.
Before the entry runs, the environment, files, sockets, subprocesses, the clock and randomness are
replaced with stubs that raise naming the capability, so module-scope effects fail with the
application's file and line. The interpreter is the application's environment when that has mcp,
else the tooling's; the CLI chooses.
"""

from __future__ import annotations

import builtins
import datetime
import functools
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
import tempfile
import time
import traceback
from collections.abc import Callable, Iterator, Mapping, Sequence
from pathlib import Path
from types import FrameType, ModuleType
from typing import TYPE_CHECKING, Any, NoReturn

if TYPE_CHECKING:
    from mcp.server import MCPServer, Server

RESOURCE_SCHEMA = "sh.holon/outputSchema"
# Standard-library modules application code may import. The others reach the host or are not
# needed to declare tools; modules already loaded by the SDK are stubbed instead (see strip).
PURE = frozenset(  # a word list, not one name per line
    "__future__ abc annotationlib argparse array ast asyncio base64 binascii bisect "  # noqa: SIM905
    "builtins calendar cmath codecs collections colorsys contextlib contextvars copy "
    "dataclasses datetime decimal difflib enum fnmatch fractions functools gettext "
    "graphlib hashlib heapq hmac html importlib inspect io ipaddress itertools json "
    "keyword linecache logging math numbers operator os pathlib pprint queue random re "
    "reprlib secrets statistics string stringprep struct sys textwrap threading time "
    "token tokenize tomllib traceback types typing unicodedata uuid warnings weakref zlib "
    "zoneinfo".split()
)
INSTALLED = {"site-packages", "dist-packages", ".venv", "venv", "node_modules"}
STDLIB = Path(sysconfig.get_paths()["stdlib"]).resolve()


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


class Guard:
    """Decides whether a guarded call comes from application code. Deciding resolves paths,
    which calls guarded functions such as os.stat on Python 3.11, so those calls go through."""

    def __init__(self, application: Callable[[str | None], bool]) -> None:
        self.application = application
        self.deciding = False

    def __call__(self) -> bool:
        if self.deciding:
            return False
        self.deciding = True
        try:
            return self.application(caller())
        finally:
            self.deciding = False


class Environ(Mapping[str, str]):
    """An environment application code cannot read. Libraries, such as Pydantic reading its
    settings while application models are defined, see it empty."""

    def __init__(self, guard: Guard) -> None:
        self.guard = guard

    def check(self, name: str = "os.environ") -> None:
        if self.guard():
            unavailable(name, "environment")()

    def getenv(self, key: str, default: str | None = None) -> str | None:
        self.check("os.getenv")
        return default

    def __getitem__(self, key: str) -> str:
        self.check()
        raise KeyError(key)

    def __iter__(self) -> Iterator[str]:
        self.check()
        return iter(())

    def __len__(self) -> int:
        self.check()
        return 0


class Finder(importlib.abc.MetaPathFinder):
    """Serves the application's files and installed packages, recording application files."""

    def __init__(self, root: Path, entry: Path) -> None:
        self.root = root
        packages = installed()
        self.search = list(dict.fromkeys([str(root), str(entry.parent), *packages]))
        self.served: set[Path] = set()
        # importlib.metadata finds distributions on sys.path; imports still go through find_spec.
        sys.path.extend(
            directory for directory in packages if directory not in sys.path
        )

    def application(self, file: str | None) -> bool:
        if not file:
            return False
        path = Path(file).resolve()
        if not path.is_relative_to(self.root):
            return False
        return not INSTALLED & set(path.relative_to(self.root).parts)

    def find_spec(
        self, name: str, path: Sequence[str] | None, target: ModuleType | None = None
    ) -> importlib.machinery.ModuleSpec | None:
        if path is None and name in sys.stdlib_module_names:
            if name not in PURE and self.application(importer()):
                raise ImportError(
                    f"{name} is unavailable: contract evaluation allows application code "
                    "only pure standard-library modules",
                    name=name,
                )
            return None
        search = self.search if path is None else path
        spec = importlib.machinery.PathFinder.find_spec(name, search, target)
        if spec is None and path is None:
            raise MissingModule(
                f"No module named {name!r}; install the application's dependencies "
                "or commit holon-contract.json",
                name=name,
            )
        if spec is not None and self.application(spec.origin):
            self.served.add(Path(str(spec.origin)).resolve())
        return spec


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


def importer() -> str | None:
    """The file whose import statement is being resolved."""
    frame: FrameType | None = sys._getframe(2)
    while frame is not None and frame.f_code.co_filename.startswith("<frozen"):
        frame = frame.f_back
    return frame.f_code.co_filename if frame is not None else None


# The guards' own functions, as opposed to the runner's work such as the finder resolving paths.
GUARDS = {"caller", "__call__", "call", "initialize", "check", "getenv", "__getitem__"}
GUARDS |= {"__iter__", "__len__"}


def caller() -> str | None:
    """The innermost file on the stack outside the guards and the standard library, or None
    for the runner's own work."""
    frame: FrameType | None = sys._getframe(1)
    while frame is not None:
        code = frame.f_code
        if code.co_filename == __file__:
            if code.co_name not in GUARDS:
                return None
        elif not code.co_filename.startswith("<") and not is_stdlib(code.co_filename):
            return code.co_filename
        frame = frame.f_back
    return None


@functools.cache
def is_stdlib(file: str) -> bool:
    return Path(file).resolve().is_relative_to(STDLIB)


def strip(guard: Guard) -> dict[tuple[object, str], object]:
    """Guard effectful APIs and return the originals. A call from application code raises; a
    call from an installed library, such as the SDK drawing request-state keys, goes through."""
    replaced: dict[tuple[object, str], object] = {}

    def guarded(
        real: Callable[..., Any], name: str, capability: str
    ) -> Callable[..., Any]:
        def call(*args: Any, **kwargs: Any) -> Any:
            if guard():
                unavailable(name, capability)()
            return real(*args, **kwargs)

        if not isinstance(real, type):
            return call

        # A class stays a class, so annotations such as subprocess.Popen[bytes] still evaluate.
        def initialize(self: object, *args: Any, **kwargs: Any) -> None:
            if guard():
                unavailable(name, capability)()
            real.__init__(self, *args, **kwargs)  # type: ignore[misc]  # the guarded base

        return type(
            real.__name__,
            (real,),
            {"__init__": initialize, "__module__": real.__module__},
        )

    def stub(owner: ModuleType, names: str, capability: str) -> None:
        for name in names.split():
            replaced[(owner, name)] = real = getattr(owner, name)
            prefix = "" if owner is builtins else f"{owner.__name__}."
            setattr(owner, name, guarded(real, f"{prefix}{name}", capability))

    replaced[(os, "environ")] = os.environ
    environ = Environ(guard)
    replaced[(os, "getenv")] = os.getenv
    os.environ, os.getenv = environ, environ.getenv  # type: ignore[assignment]
    stub(os, "putenv unsetenv", "environment")
    stub(builtins, "open", "filesystem")
    stub(io, "open", "filesystem")
    stub(os, "open listdir scandir stat remove unlink mkdir makedirs", "filesystem")
    stub(os, "rename replace rmdir getcwd", "filesystem")
    stub(socket, "socket socketpair create_connection getaddrinfo", "network")
    stub(subprocess, "Popen run call check_call check_output", "child processes")
    stub(os, "system popen execv execve", "child processes")
    stub(time, "time time_ns localtime gmtime ctime", "clock")
    stub(random, "random randint randrange choice choices shuffle sample", "randomness")
    stub(random, "uniform getrandbits", "randomness")
    stub(os, "urandom", "randomness")
    for cls in (datetime.datetime, datetime.date):
        replaced[(datetime, cls.__name__)] = cls
        clock = {
            name: classmethod(
                guarded(getattr(cls, name), f"{cls.__name__}.{name}", "clock")
            )
            for name in ("now", "utcnow", "today")
            if hasattr(cls, name)
        }
        setattr(datetime, cls.__name__, type(cls.__name__, (cls,), clock))
    return replaced


def restore(replaced: dict[tuple[object, str], object]) -> None:
    for (owner, name), value in replaced.items():
        setattr(owner, name, value)


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
    """Run one entry, in the child process, and ask its server for the catalog."""
    path = root / entry
    finder = Finder(root, path)
    sys.meta_path.insert(0, finder)
    import asyncio

    try:
        replaced = strip(Guard(finder.application))
        try:
            # Import the entry as a script beside its project would, without its __main__ block.
            spec = importlib.util.spec_from_file_location(path.stem, path)
            if spec is None or spec.loader is None:
                raise ImportError(f"cannot import {path}")
            finder.served.add(path.resolve())
            module = sys.modules[path.stem] = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)
            result = asyncio.run(catalog(find_server(module)))
        finally:
            restore(replaced)
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
        failure = {
            "diagnostic": f"{entry}: Traceback (most recent call last):\n{''.join(trace)}"
        }
        if isinstance(error, MissingModule) and error.name:
            failure["missing"] = error.name.partition(".")[0]
        return failure
    sources = {}
    for file in sorted(finder.served):
        with file.open("rb") as source:
            digest = hashlib.file_digest(source, "sha256").hexdigest()
        sources[file.relative_to(root).as_posix()] = digest
    return {**result, "sources": sources}


def extract(root: str | Path, entry: str) -> dict[str, Any]:
    timeout = float(os.environ.get("HOLON_CONTRACT_TIMEOUT") or 30)
    with tempfile.TemporaryDirectory() as directory:
        output = Path(directory, "contract.json")
        command = [sys.executable, "-I", "-S", __file__, "--contract", str(root), entry]
        try:
            subprocess.run(
                [*command, str(output)], cwd=root, timeout=timeout, check=False
            )
        except subprocess.TimeoutExpired:
            return {
                "diagnostic": f"{entry}: no catalog within {timeout:g}s; raise "
                "HOLON_CONTRACT_TIMEOUT if module-scope work needs longer"
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
