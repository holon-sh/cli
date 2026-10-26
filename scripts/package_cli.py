#!/usr/bin/env python3
"""Build CLI tarballs, wheels and npm packages. Never publishes."""

from __future__ import annotations

import argparse
import base64
import csv
import gzip
import hashlib
import io
import json
import os
import re
import struct
import subprocess
import tarfile
import tempfile
import zipfile
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


def write_tarball(binary: Path, archive: Path) -> None:
    info = tarfile.TarInfo("holon")
    info.size, info.mode, info.mtime = binary.stat().st_size, 0o755, 0
    with archive.open("wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz, tarfile.open(fileobj=gz, mode="w") as tar, binary.open("rb") as source:
        tar.addfile(info, source)


def verify_binary(binary: Path, target: Target) -> tuple[int, int] | None:
    """Never label a dynamic Linux executable as compatible with musl, or a macOS one with macOS 15."""
    data = binary.read_bytes()
    if target.goos == "darwin":
        if data[:4] != b"\xcf\xfa\xed\xfe":
            raise ValueError("expected a 64-bit Mach-O binary")
        cpu = struct.unpack_from("<I", data, 4)[0]
        if cpu != {"arm64": 0x0100000C, "amd64": 0x01000007}[target.goarch]:
            raise ValueError("Mach-O architecture does not match target")
        offset = 32
        minimum = None
        for _ in range(struct.unpack_from("<I", data, 16)[0]):
            command, size = struct.unpack_from("<II", data, offset)
            if command in (0x24, 0x32):
                version_offset = 12 if command == 0x32 else 8
                minimum = max(minimum or 0, struct.unpack_from("<I", data, offset + version_offset)[0])
                if minimum > 15 << 16:
                    raise ValueError("Mach-O binary requires newer than macOS 15.0; rebuild with MACOSX_DEPLOYMENT_TARGET=15.0")
            offset += size
        if minimum is None:
            raise ValueError("Mach-O binary has no minimum macOS version")
        return minimum >> 16, (minimum >> 8) & 0xFF
    if data[:6] != b"\x7fELF\x02\x01":
        raise ValueError("expected a little-endian 64-bit ELF binary")
    if struct.unpack_from("<H", data, 18)[0] != {"arm64": 183, "amd64": 62}[target.goarch]:
        raise ValueError("ELF architecture does not match target")
    offset = struct.unpack_from("<Q", data, 32)[0]
    size, count = struct.unpack_from("<HH", data, 54)
    for index in range(count):
        if struct.unpack_from("<I", data, offset + index * size)[0] in (2, 3):
            raise ValueError("Linux binaries must be static (no PT_DYNAMIC or PT_INTERP)")


def build_binary(binary: Path, target: Target, version: str) -> None:
    """Cross-compile from any host: the CLI is pure Go, so the binary is static without a C toolchain."""
    env = os.environ | {"CGO_ENABLED": "0", "GOOS": target.goos, "GOARCH": target.goarch}
    subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", f"-s -w -X main.version={version}", "-o", str(binary), "./cmd/holon"], cwd=ROOT, env=env, check=True)


def python_version(version: str) -> str:
    """Translate the CLI's prerelease version into PEP 440."""
    if "-" not in version:
        return version
    release, prerelease = version.split("-")
    kind, number = prerelease.split(".")
    suffix = {"alpha": "a", "beta": "b", "rc": "rc", "dev": ".dev"}[kind]
    return f"{release}{suffix}{number}"


def wheel_tags(target: Target, minimum_macos: tuple[int, int] | None) -> list[str]:
    arch = "x86_64" if target.goarch == "amd64" else "aarch64"
    if target.goos == "linux":
        return [f"manylinux_2_17_{arch}", f"manylinux2014_{arch}", f"musllinux_1_2_{arch}"]
    if minimum_macos is None:
        raise ValueError("macOS wheel requires the binary's minimum OS version")
    major, minor = minimum_macos
    return [f"macosx_{major}_{minor}_{'arm64' if target.goarch == 'arm64' else arch}"]


def write_wheel(binary: Path, dist: Path, version: str, tags: list[str]) -> None:
    version = python_version(version)
    info = f"holon_types-{version}.dist-info"
    metadata = (
        "Metadata-Version: 2.1\n"
        "Name: holon-types\n"
        f"Version: {version}\n"
        "Summary: The holon CLI: typed clients for MCP servers, generated and type-checked\n"
        "Home-page: https://github.com/holon-sh/cli\n"
        "License: MIT\n"
        "Requires-Python: >=3.11\n"
        "Description-Content-Type: text/markdown\n\n"
        + (ROOT / "packaging/pypi/README.md").read_text()
    )
    wheel = "Wheel-Version: 1.0\nGenerator: holon-package-cli\nRoot-Is-Purelib: false\n"
    wheel += "".join(f"Tag: py3-none-{tag}\n" for tag in tags)
    entries = [
        ("holon_types/__init__.py", (ROOT / "packaging/pypi/holon_types/__init__.py").read_bytes(), 0o644),
        ("holon_types/bin/holon", binary.read_bytes(), 0o755),
        (f"{info}/METADATA", metadata.encode(), 0o644),
        (f"{info}/WHEEL", wheel.encode(), 0o644),
        (f"{info}/entry_points.txt", b"[console_scripts]\nholon = holon_types:main\n", 0o644),
        (f"{info}/LICENSE", (ROOT / "LICENSE").read_bytes(), 0o644),
    ]
    record = io.StringIO(newline="")
    writer = csv.writer(record, lineterminator="\n")
    for name, data, _ in entries:
        digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
        writer.writerow([name, f"sha256={digest}", len(data)])
    writer.writerow([f"{info}/RECORD", "", ""])
    entries.append((f"{info}/RECORD", record.getvalue().encode(), 0o644))
    archive = dist / f"holon_types-{version}-py3-none-{'.'.join(tags)}.whl"
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as wheel_file:
        for name, data, mode in entries:
            entry = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            entry.create_system = 3
            entry.external_attr = mode << 16
            wheel_file.writestr(entry, data, compress_type=zipfile.ZIP_DEFLATED)


def write_npm_package(archive: Path, manifest: dict, executable: str, data: bytes, readme: Path) -> None:
    entries = [
        ("package.json", (json.dumps(manifest, indent=2) + "\n").encode(), 0o644),
        (executable, data, 0o755),
        ("README.md", readme.read_bytes(), 0o644),
        ("LICENSE", (ROOT / "LICENSE").read_bytes(), 0o644),
    ]
    with archive.open("wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz, tarfile.open(fileobj=gz, mode="w") as tar:
        for name, contents, mode in entries:
            entry = tarfile.TarInfo(f"package/{name}")
            entry.size, entry.mode, entry.mtime = len(contents), mode, 0
            tar.addfile(entry, io.BytesIO(contents))


def write_npm_launcher(dist: Path, version: str) -> None:
    source = ROOT / "packaging/npm/launcher"
    manifest = json.loads((source / "package.json").read_text())
    manifest["version"] = version
    manifest["optionalDependencies"] = dict.fromkeys(manifest["optionalDependencies"], version)
    write_npm_package(dist / f"holon-types-{version}.tgz", manifest, "bin/holon.js", (source / "bin/holon.js").read_bytes(), source / "README.md")


def write_npm_platform(binary: Path, dist: Path, version: str, target: Target) -> None:
    manifest = {
        "name": target.npm_name,
        "version": version,
        "description": f"holon CLI binary for {target.goos}-{target.npm_cpu}; install holon-types instead",
        "repository": {"type": "git", "url": "git+https://github.com/holon-sh/cli.git"},
        "license": "MIT",
        "os": [target.goos],
        "cpu": [target.npm_cpu],
        "files": ["bin/holon", "README.md", "LICENSE"],
        "publishConfig": {"access": "public"},
    }
    write_npm_package(dist / f"{target.npm_name}-{version}.tgz", manifest, "bin/holon", binary.read_bytes(), ROOT / "packaging/npm/platform/README.md")


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
        minimum_macos = verify_binary(binary, target)
        if "tarball" in formats:
            write_tarball(binary, dist / f"holon_{args.version}_{target.platform}.tar.gz")
        if "wheel" in formats:
            write_wheel(binary, dist, args.version, wheel_tags(target, minimum_macos))
        if "npm-platform" in formats:
            write_npm_platform(binary, dist, args.version, target)
        if "npm-launcher" in formats:
            write_npm_launcher(dist, args.version)


if __name__ == "__main__":
    main()
