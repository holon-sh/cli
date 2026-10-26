import configparser
import hashlib
import json
import os
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile
from email.parser import BytesParser
from pathlib import Path

from scripts import package_cli

ROOT = Path(__file__).resolve().parent.parent
VERSION = "1.2.3-rc.1"
PYVERSION = "1.2.3rc1"


class PackageTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        directory = tempfile.TemporaryDirectory(prefix="holon-packages-")
        cls.addClassCleanup(directory.cleanup)
        cls.directory = Path(directory.name)
        host = subprocess.check_output(["go", "env", "GOHOSTOS", "GOHOSTARCH"], text=True).split()
        cls.target = package_cli.Target(*host)
        cls.binary = cls.directory / "prebuilt holon"
        package_cli.build_binary(cls.binary, cls.target, VERSION)
        cls.dist = cls.directory / "first"
        cls.package(cls.dist)

    @classmethod
    def package(cls, output, formats=("tarball", "wheel", "npm-platform", "npm-launcher")):
        command = [sys.executable, str(ROOT / "scripts/package_cli.py"), "--target", cls.target.platform,
                   "--version", VERSION, "--binary", str(cls.binary), "--out", str(output)]
        for kind in formats:
            command.extend(["--format", kind])
        subprocess.run(command, check=True, capture_output=True, text=True)

    def test_wheel_contents_and_integrity(self):
        info = f"holon_types-{PYVERSION}.dist-info"
        path = next(self.dist.glob("*.whl"))
        with zipfile.ZipFile(path) as wheel:
            expected = ["holon_types/__init__.py", "holon_types/bin/holon", f"{info}/METADATA",
                        f"{info}/WHEEL", f"{info}/entry_points.txt", f"{info}/licenses/LICENSE", f"{info}/RECORD"]
            self.assertEqual(set(wheel.namelist()), set(expected))
            self.assertEqual(wheel.read("holon_types/bin/holon"), self.binary.read_bytes())
            self.assertEqual(wheel.read(f"{info}/licenses/LICENSE"), (ROOT / "LICENSE").read_bytes())
            for entry in wheel.infolist():
                mode = entry.external_attr >> 16
                self.assertTrue(mode & 0o400)
                self.assertEqual(mode & 0o111, 0o111 if entry.filename.endswith("/bin/holon") else 0)
            metadata = BytesParser().parsebytes(wheel.read(f"{info}/METADATA"))
            self.assertEqual((metadata["Name"], metadata["Version"], metadata["Requires-Python"]), ("holon-types", PYVERSION, ">=3.11"))
            self.assertEqual(metadata.get_payload(), (ROOT / "packaging/README.md").read_text())
            entry_points = configparser.ConfigParser()
            entry_points.read_string(wheel.read(f"{info}/entry_points.txt").decode())
            self.assertEqual(dict(entry_points["console_scripts"]), {"holon": "holon_types:main"})
            wheel_metadata = BytesParser().parsebytes(wheel.read(f"{info}/WHEEL"))
            self.assertEqual(wheel_metadata["Root-Is-Purelib"], "false")
            tags = [platform for tag in wheel_metadata.get_all("Tag") for platform in tag.removeprefix("py3-none-").split(".")]
            self.assertEqual(path.name, f"holon_types-{PYVERSION}-py3-none-{'.'.join(tags)}.whl")
            arch = "x86_64" if self.target.goarch == "amd64" else "aarch64"
            if self.target.goos == "linux":
                self.assertEqual(tags, [f"manylinux_2_17_{arch}", f"manylinux2014_{arch}", f"musllinux_1_2_{arch}"])
            else:
                self.assertEqual(len(tags), 1)
                arch = "arm64" if self.target.goarch == "arm64" else arch
                self.assertRegex(tags[0], rf"^macosx_\d+_\d+_{arch}$")
            self.assertIsNone(wheel.testzip())

    def test_npm_packages(self):
        cpu = "x64" if self.target.goarch == "amd64" else "arm64"
        platform_name = f"holon-types-{self.target.goos}-{cpu}"
        for name, executable in ((platform_name, "bin/holon"), ("holon-types", "bin/holon.js")):
            with self.subTest(package=name), tarfile.open(self.dist / f"{name}-{VERSION}.tgz") as archive:
                self.assertEqual(set(archive.getnames()), {"package/package.json", f"package/{executable}",
                                                          "package/README.md", "package/LICENSE"})
                for entry in archive.getmembers():
                    self.assertEqual(entry.mode, 0o755 if entry.name == f"package/{executable}" else 0o644)
                self.assertEqual(archive.extractfile("package/LICENSE").read(), (ROOT / "LICENSE").read_bytes())
                manifest = json.load(archive.extractfile("package/package.json"))
                self.assertEqual(manifest["name"], name)
                self.assertEqual(manifest["version"], VERSION)
                self.assertNotIn("scripts", manifest)
                if name == "holon-types":
                    self.assertEqual(manifest["bin"], {"holon": "bin/holon.js"})
                    self.assertEqual(manifest["engines"], {"node": ">=22"})
                    self.assertEqual(manifest["optionalDependencies"], {
                        "holon-types-darwin-arm64": VERSION, "holon-types-darwin-x64": VERSION,
                        "holon-types-linux-arm64": VERSION, "holon-types-linux-x64": VERSION,
                    })
                else:
                    self.assertEqual(manifest["os"], [self.target.goos])
                    self.assertEqual(manifest["cpu"], [cpu])
                    self.assertEqual(archive.extractfile("package/bin/holon").read(), self.binary.read_bytes())

    def test_reproducible_packages(self):
        second = self.directory / "second"
        self.package(second)
        self.assertEqual(len(list(second.iterdir())), 4)
        self.assertEqual({p.name: hashlib.sha256(p.read_bytes()).digest() for p in self.dist.iterdir()},
                         {p.name: hashlib.sha256(p.read_bytes()).digest() for p in second.iterdir()})

    def test_default_is_still_the_standalone_tarball(self):
        output = self.directory / "default"
        self.package(output, formats=())
        name = f"holon_{VERSION}_{self.target.platform}.tar.gz"
        self.assertEqual([p.name for p in output.iterdir()], [name])
        with tarfile.open(output / name) as archive:
            self.assertEqual(archive.getnames(), ["holon"])
            self.assertEqual(archive.getmember("holon").mode, 0o755)
            self.assertEqual(archive.extractfile("holon").read(), self.binary.read_bytes())


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

    def test_launcher_needs_neither_target_nor_go(self):
        with tempfile.TemporaryDirectory() as directory:
            tools = Path(directory) / "tools"
            tools.mkdir()
            for name in ("node", "npm"):
                (tools / name).symlink_to(Path(shutil.which(name)).resolve())
            subprocess.run([sys.executable, str(ROOT / "scripts/package_cli.py"), "--format", "npm-launcher",
                            "--out", directory, "--version", VERSION], env=os.environ | {"PATH": str(tools)}, check=True)
            self.assertEqual([p.name for p in Path(directory).glob("*.tgz")], [f"holon-types-{VERSION}.tgz"])

    def test_binary_formats_require_a_target(self):
        for kind in ("tarball", "wheel", "npm-platform"):
            with self.subTest(kind=kind):
                result = subprocess.run([sys.executable, str(ROOT / "scripts/package_cli.py"), "--format", kind],
                                        text=True, capture_output=True)
                self.assertEqual(result.returncode, 2)
                self.assertIn("--target is required", result.stderr)
