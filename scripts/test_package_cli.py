import hashlib
import json
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

from scripts import package_cli

ROOT = Path(__file__).resolve().parent.parent


class PackageTests(unittest.TestCase):
    def test_release_packages(self):
        host = subprocess.check_output(["go", "env", "GOHOSTOS", "GOHOSTARCH"], text=True).split()
        with tempfile.TemporaryDirectory() as directory:
            packages = []
            for run in ("first", "second"):
                output = Path(directory) / run
                subprocess.run([
                    sys.executable, str(ROOT / "scripts/package_cli.py"), "--target", "_".join(host),
                    "--version", "1.2.3-rc.1", "--out", str(output),
                    "--format", "tarball", "--format", "wheel", "--format", "npm-platform", "--format", "npm-launcher",
                ], check=True, capture_output=True)
                packages.append({p.name: hashlib.sha256(p.read_bytes()).digest() for p in output.iterdir()})
            self.assertEqual(len(packages[0]), 4)
            self.assertEqual(packages[0], packages[1])
            with tarfile.open(output / "holon-types-1.2.3-rc.1.tgz") as archive:
                launcher = json.load(archive.extractfile("package/package.json"))
            self.assertEqual(launcher["optionalDependencies"], {name: "1.2.3-rc.1" for name in (
                "holon-types-darwin-arm64", "holon-types-darwin-x64", "holon-types-linux-arm64", "holon-types-linux-x64",
            )})


class PackageMetadataTests(unittest.TestCase):
    def test_macos_tags_follow_both_load_command_formats(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "holon"
            for command in (0x24, 0x32):
                with self.subTest(command=command):
                    # Mach-O header and a deployment target of 12.3, independent of Go's defaults.
                    header = struct.pack("<8I", 0xFEEDFACF, 0x0100000C, 0, 2, 1, 24, 0, 0)
                    version = (12 << 16) | (3 << 8)
                    load = struct.pack("<6I", command, 24, 1, version, 0, 0) if command == 0x32 else struct.pack("<6I", command, 24, version, 0, 0, 0)
                    binary.write_bytes(header + load)
                    target = package_cli.Target("darwin", "arm64")
                    self.assertEqual(package_cli.verify_binary(binary, target), ["macosx_12_3_arm64"])
            binary.write_bytes(struct.pack("<8I", 0xFEEDFACF, 0x0100000C, 0, 2, 0, 0, 0, 0))
            with self.assertRaisesRegex(ValueError, "no minimum macOS version"):
                package_cli.verify_binary(binary, package_cli.Target("darwin", "arm64"))

    def test_dynamic_linux_binaries_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "holon"
            for program_type in (2, 3):
                with self.subTest(program_type=program_type):
                    data = bytearray(120)
                    data[:6] = b"\x7fELF\x02\x01"
                    struct.pack_into("<H", data, 18, 62)
                    struct.pack_into("<Q", data, 32, 64)
                    struct.pack_into("<HH", data, 54, 56, 1)
                    struct.pack_into("<I", data, 64, program_type)
                    binary.write_bytes(data)
                    with self.assertRaisesRegex(ValueError, "must be static"):
                        package_cli.verify_binary(binary, package_cli.Target("linux", "amd64"))
