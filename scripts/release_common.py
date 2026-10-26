"""Platform metadata shared by local and CI release packaging."""

import hashlib
import re
from pathlib import Path
from typing import NamedTuple


class Target(NamedTuple):
    goos: str
    goarch: str

    @property
    def platform(self) -> str:
        return f"{self.goos}_{self.goarch}"


TARGETS = tuple(Target(os, arch) for os in ("darwin", "linux") for arch in ("amd64", "arm64"))
VERSION_PATTERN = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+(?:-(?:alpha|beta|rc|dev)\.[0-9]+)?")


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()
