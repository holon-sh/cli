#!/usr/bin/env python3
"""Build CLI tarballs, wheels and npm packages. Never publishes."""

from __future__ import annotations

import argparse
import gzip
import json
import os
import re
import shutil
import subprocess
import tarfile
import tempfile
from contextlib import chdir
from pathlib import Path
from typing import NamedTuple

ROOT = Path(__file__).resolve().parent.parent


class Target(NamedTuple):
    goos: str
    goarch: str

    @property
    def platform(self) -> str:
        return f"{self.goos}_{self.goarch}"

    @property
    def npm_cpu(self) -> str:
        return "x64" if self.goarch == "amd64" else "arm64"

    @property
    def npm_name(self) -> str:
        return f"holon-types-{self.goos}-{self.npm_cpu}"


TARGETS = tuple(Target(os, arch) for os in ("darwin", "linux") for arch in ("amd64", "arm64"))
VERSION_PATTERN = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+(?:-(?:alpha|beta|rc|dev)\.[0-9]+)?")


def write_tarball(archive: Path, binary: Path) -> None:
    with archive.open("wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz, tarfile.open(fileobj=gz, mode="w") as tar, binary.open("rb") as executable:
        entry = tarfile.TarInfo("holon")
        entry.size, entry.mode = binary.stat().st_size, 0o755
        tar.addfile(entry, executable)


def verify_binary(binary: Path, target: Target) -> list[str]:
    """Return verified wheel tags: never label dynamic Linux as musl-compatible or guess macOS support."""
    if target.goos == "darwin":
        from macholib import mach_o
        from macholib.MachO import MachO

        executable = MachO(str(binary))
        header = executable.headers[0]
        if executable.fat or not isinstance(header.header, mach_o.mach_header_64) or header.endian != "<":
            raise ValueError("expected a thin little-endian 64-bit Mach-O binary")
        if mach_o.CPU_TYPE_NAMES.get(header.header.cputype) != {"arm64": "ARM64", "amd64": "x86_64"}[target.goarch]:
            raise ValueError("Mach-O architecture does not match target")
        version_fields = {mach_o.LC_VERSION_MIN_MACOSX: "version", mach_o.LC_BUILD_VERSION: "minos"}
        versions = [getattr(command, version_fields[load.cmd]) for load, command, _ in header.commands if load.cmd in version_fields]
        if not versions:
            raise ValueError("Mach-O binary has no minimum macOS version")
        minimum = mach_o.mach_version_helper(max(versions))
        if (minimum.major, minimum.minor, minimum.rev) > (15, 0, 0):
            raise ValueError("Mach-O binary requires newer than macOS 15.0; rebuild with MACOSX_DEPLOYMENT_TARGET=15.0")
        arch = "x86_64" if target.goarch == "amd64" else "arm64"
        return [f"macosx_{minimum.major}_{minimum.minor}_{arch}"]
    from elftools.elf.elffile import ELFFile

    with binary.open("rb") as stream:
        executable = ELFFile(stream)
        if executable.elfclass != 64 or not executable.little_endian:
            raise ValueError("expected a little-endian 64-bit ELF binary")
        if executable["e_machine"] != {"arm64": "EM_AARCH64", "amd64": "EM_X86_64"}[target.goarch]:
            raise ValueError("ELF architecture does not match target")
        if any(segment["p_type"] in ("PT_DYNAMIC", "PT_INTERP") for segment in executable.iter_segments()):
            raise ValueError("Linux binaries must be static (no PT_DYNAMIC or PT_INTERP)")
    arch = "x86_64" if target.goarch == "amd64" else "aarch64"
    return [f"manylinux_2_17_{arch}", f"manylinux2014_{arch}", f"musllinux_1_2_{arch}"]


def build_binary(binary: Path, target: Target, version: str) -> None:
    """Cross-compile from any host: the CLI is pure Go, so the binary is static without a C toolchain."""
    env = os.environ | {"CGO_ENABLED": "0", "GOOS": target.goos, "GOARCH": target.goarch}
    subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", f"-s -w -X main.version={version}", "-o", str(binary), "./cmd/holon"], cwd=ROOT, env=env, check=True)


def write_wheel(binary: Path, dist: Path, version: str, tags: list[str]) -> None:
    from pdm.backend import build_wheel
    from pdm.backend.config import Config

    with tempfile.TemporaryDirectory(prefix="holon-wheel-") as directory:
        package = Path(directory)
        source = ROOT / "packaging/pypi"
        shutil.copytree(source / "holon_types", package / "holon_types")
        (package / "holon_types/bin").mkdir()
        shutil.copyfile(binary, package / "holon_types/bin/holon")
        (package / "holon_types/bin/holon").chmod(0o755)
        shutil.copyfile(ROOT / "README.md", package / "README.md")
        shutil.copyfile(ROOT / "LICENSE", package / "LICENSE")
        shutil.copyfile(source / "pyproject.toml", package / "pyproject.toml")
        config = Config.from_pyproject(package)
        config.metadata["version"] = version
        config.write_to(package / "pyproject.toml")
        with chdir(package):
            build_wheel(str(dist), {"--python-tag": "py3", "--py-limited-api": "none", "--plat-name": tags})


def write_npm_package(dist: Path, manifest: dict, source: Path, executable: str, readme: Path) -> None:
    with tempfile.TemporaryDirectory(prefix="holon-npm-") as directory:
        package = Path(directory)
        (package / "bin").mkdir()
        shutil.copyfile(source, package / "bin" / executable)
        (package / "bin" / executable).chmod(0o755)
        shutil.copyfile(readme, package / "README.md")
        shutil.copyfile(ROOT / "LICENSE", package / "LICENSE")
        (package / "package.json").write_text(json.dumps(manifest, indent=2) + "\n")
        subprocess.run(["npm", "pack", "--ignore-scripts", "--offline", "--pack-destination", str(dist)], cwd=package, check=True)


def write_npm_launcher(dist: Path, version: str) -> None:
    source = ROOT / "packaging/npm/launcher"
    manifest = json.loads((source / "package.json").read_text())
    manifest["version"] = version
    manifest["optionalDependencies"] = {target.npm_name: version for target in TARGETS}
    write_npm_package(dist, manifest, source / "bin/holon.js", "holon.js", ROOT / "README.md")


def write_npm_platform(binary: Path, dist: Path, version: str, target: Target) -> None:
    manifest = {
        "name": target.npm_name,
        "version": version,
        "description": f"holon CLI binary for {target.goos}-{target.npm_cpu}; install holon-types instead",
        "repository": {"type": "git", "url": "git+https://github.com/holon-sh/cli.git"},
        "license": "MIT",
        "os": [target.goos],
        "cpu": [target.npm_cpu],
    }
    write_npm_package(dist, manifest, binary, "holon", ROOT / "packaging/npm/platform/README.md")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default="0.0.1")
    parser.add_argument("--target", choices=[target.platform for target in TARGETS])
    parser.add_argument("--binary", type=Path, help="Package a prebuilt binary instead of building one")
    parser.add_argument("--format", action="append", choices=["tarball", "wheel", "npm-platform", "npm-launcher"], dest="formats")
    parser.add_argument("--out", type=Path, default=ROOT / "dist")
    args = parser.parse_args()
    if not VERSION_PATTERN.fullmatch(args.version):
        parser.error("version must be X.Y.Z or X.Y.Z-{alpha,beta,rc,dev}.N")
    formats = set(args.formats or ["tarball"])
    if formats - {"npm-launcher"} and not args.target:
        parser.error("--target is required for tarball, wheel and npm-platform")
    dist = args.out.resolve()
    dist.mkdir(parents=True, exist_ok=True)
    if formats == {"npm-launcher"}:
        write_npm_launcher(dist, args.version)
        return
    target = Target(*args.target.split("_"))
    with tempfile.TemporaryDirectory(prefix="holon-package-") as directory:
        binary = args.binary or Path(directory) / "holon"
        if args.binary is None:
            build_binary(binary, target, args.version)
        tags = verify_binary(binary, target)
        if "tarball" in formats:
            write_tarball(dist / f"holon_{args.version}_{target.platform}.tar.gz", binary)
        if "wheel" in formats:
            write_wheel(binary, dist, args.version, tags)
        if "npm-platform" in formats:
            write_npm_platform(binary, dist, args.version, target)
        if "npm-launcher" in formats:
            write_npm_launcher(dist, args.version)


if __name__ == "__main__":
    main()
