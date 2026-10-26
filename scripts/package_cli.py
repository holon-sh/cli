#!/usr/bin/env python3
"""Build/package one CLI platform for npm, PyPI, and direct download. Never publishes."""

import argparse
import gzip
import json
import os
import shutil
import struct
import subprocess
import tarfile
import tempfile
from pathlib import Path

from release_common import TARGETS, Target, python_version, sha256

ROOT = Path(__file__).resolve().parent.parent


def write_tarball(binary: Path, archive: Path) -> None:
    info = tarfile.TarInfo("holon")
    info.size, info.mode, info.mtime = binary.stat().st_size, 0o755, 0
    with archive.open("wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz, tarfile.open(fileobj=gz, mode="w") as tar, binary.open("rb") as source:
        tar.addfile(info, source)


def verify_binary(binary: Path, target: Target) -> None:
    """Never label a dynamic Linux executable as compatible with musl, or a macOS one with macOS 15."""
    data = binary.read_bytes()
    if target.goos == "darwin":
        if data[:4] != b"\xcf\xfa\xed\xfe":
            raise ValueError("expected a 64-bit Mach-O binary")
        cpu = struct.unpack_from("<I", data, 4)[0]
        if cpu != {"arm64": 0x0100000C, "amd64": 0x01000007}[target.goarch]:
            raise ValueError("Mach-O architecture does not match target")
        offset = 32
        for _ in range(struct.unpack_from("<I", data, 16)[0]):
            command, size = struct.unpack_from("<II", data, offset)
            if command in (0x24, 0x32):
                version_offset = 12 if command == 0x32 else 8
                minimum = struct.unpack_from("<I", data, offset + version_offset)[0]
                if minimum > 15 << 16:
                    raise ValueError("Mach-O binary requires newer than macOS 15.0; rebuild with MACOSX_DEPLOYMENT_TARGET=15.0")
            offset += size
        return
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


def build_wheel(stage: Path, dist: Path, binary: Path, target: Target, version: str) -> None:
    project = stage / "pypi"
    shutil.copytree(ROOT / "packaging/pypi", project)
    shutil.copyfile(ROOT / "LICENSE", project / "LICENSE")
    # The repository README (the tutorial) is the PyPI long description.
    shutil.copyfile(ROOT / "README.md", project / "README.md")
    manifest = project / "pyproject.toml"
    manifest.write_text(manifest.read_text().replace('version = "0.0.1"', f'version = "{python_version(version)}"'))
    destination = project / "holon_types/bin/holon"
    destination.parent.mkdir(parents=True)
    shutil.copyfile(binary, destination)
    destination.chmod(0o755)
    subprocess.run(
        ["uv", "build", "--wheel", "--out-dir", str(dist), str(project)],
        env=os.environ | {"HOLON_WHEEL_TAG": f"py3-none-{target.wheel_platform}"},
        check=True,
    )


def build_npm(stage: Path, dist: Path, binary: Path, target: Target, version: str) -> None:
    platform = stage / "npm-platform"
    (platform / "bin").mkdir(parents=True)
    shutil.copyfile(binary, platform / "bin/holon")
    (platform / "bin/holon").chmod(0o755)
    shutil.copyfile(ROOT / "LICENSE", platform / "LICENSE")
    manifest = {"name": f"holon-types-{target.goos}-{target.npm_arch}", "version": version, "description": "Holon standalone CLI platform binary", "repository": {"type": "git", "url": "git+https://github.com/holon-sh/cli.git"}, "license": "MIT", "os": [target.goos], "cpu": [target.npm_arch], "files": ["bin/holon", "LICENSE"], "exports": {"./bin/holon": "./bin/holon"}, "publishConfig": {"access": "public"}}
    (platform / "package.json").write_text(json.dumps(manifest, indent=2) + "\n")
    package = stage / "npm"
    shutil.copytree(ROOT / "packaging/npm", package)
    shutil.copyfile(ROOT / "LICENSE", package / "LICENSE")
    manifest = json.loads((package / "package.json").read_text())
    manifest["version"] = version
    manifest["optionalDependencies"] = {name: version for name in manifest["optionalDependencies"]}
    (package / "package.json").write_text(json.dumps(manifest, indent=2) + "\n")
    for directory in (platform, package):
        subprocess.run(["npm", "pack", "--ignore-scripts", "--pack-destination", str(dist)], cwd=directory, check=True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default=os.environ.get("HOLON_VERSION", "0.0.1"))
    parser.add_argument("--target", choices=[target.platform for target in TARGETS], default=os.environ.get("HOLON_TARGET"), required="HOLON_TARGET" not in os.environ)
    parser.add_argument("--binary", type=Path, help="package an already-built binary")
    parser.add_argument("--out", type=Path, default=ROOT / "dist")
    args = parser.parse_args()
    python_version(args.version)
    target = next(target for target in TARGETS if target.platform == args.target)
    dist = args.out.resolve()
    dist.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="holon-package-") as directory:
        stage = Path(directory)
        binary = args.binary.resolve() if args.binary else stage / "holon"
        if args.binary is None:
            build_binary(binary, target, args.version)
        verify_binary(binary, target)
        write_tarball(binary, dist / f"holon_{args.version}_{target.platform}.tar.gz")
        build_wheel(stage, dist, binary, target, args.version)
        build_npm(stage, dist, binary, target, args.version)
    artifacts = sorted(path for path in dist.iterdir() if path.suffix in (".whl", ".tgz") or path.name.endswith(".tar.gz"))
    (dist / "SHA256SUMS").write_text("".join(f"{sha256(path)}  {path.name}\n" for path in artifacts))


if __name__ == "__main__":
    main()
