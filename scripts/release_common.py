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

    @property
    def npm_arch(self) -> str:
        return "x64" if self.goarch == "amd64" else "arm64"

    @property
    def wheel_platform(self) -> str:
        arch = "x86_64" if self.goarch == "amd64" else "arm64"
        if self.goos == "darwin":
            return f"macosx_15_0_{arch}"
        arch = "x86_64" if self.goarch == "amd64" else "aarch64"
        return f"manylinux_2_17_{arch}.musllinux_1_2_{arch}"


TARGETS = tuple(Target(os, arch) for os in ("darwin", "linux") for arch in ("amd64", "arm64"))
VERSION_PATTERN = r"[0-9]+\.[0-9]+\.[0-9]+(?:-(?:alpha|beta|rc|dev)\.[0-9]+)?"


def python_version(version: str) -> str:
    if not re.fullmatch(VERSION_PATTERN, version):
        raise ValueError("version must be X.Y.Z or X.Y.Z-{alpha,beta,rc,dev}.N")
    return re.sub(r"-(alpha|beta|rc|dev)\.([0-9]+)$", lambda match: {"alpha": "a", "beta": "b", "rc": "rc", "dev": ".dev"}[match[1]] + match[2], version)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()
