import base64
import csv
import hashlib
import io
import json
import os
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile
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
        cls.binary = cls.directory / "holon"
        subprocess.run([
            "go", "build", "-trimpath", "-buildvcs=false", "-ldflags", f"-s -w -X main.version={VERSION}",
            "-o", str(cls.binary), "./cmd/holon",
        ], cwd=ROOT, env=os.environ | {"CGO_ENABLED": "0", "GOOS": host[0], "GOARCH": host[1]}, check=True)
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
                        f"{info}/WHEEL", f"{info}/entry_points.txt", f"{info}/LICENSE", f"{info}/RECORD"]
            self.assertEqual(wheel.namelist(), expected)
            self.assertEqual(wheel.read("holon_types/bin/holon"), self.binary.read_bytes())
            self.assertEqual(wheel.read(f"{info}/LICENSE"), (ROOT / "LICENSE").read_bytes())
            for entry in wheel.infolist():
                self.assertEqual(entry.external_attr >> 16, 0o755 if entry.filename.endswith("/bin/holon") else 0o644)
                self.assertEqual(entry.date_time, (1980, 1, 1, 0, 0, 0))
                self.assertEqual(entry.compress_type, zipfile.ZIP_DEFLATED)
            metadata = wheel.read(f"{info}/METADATA").decode()
            for line in ("Name: holon-types", f"Version: {PYVERSION}", "Requires-Python: >=3.11"):
                self.assertIn(line + "\n", metadata)
            self.assertTrue(metadata.endswith((ROOT / "packaging/pypi/README.md").read_text()))
            self.assertEqual(wheel.read(f"{info}/entry_points.txt"), b"[console_scripts]\nholon = holon_types:main\n")
            wheel_metadata = wheel.read(f"{info}/WHEEL").decode()
            self.assertIn("Root-Is-Purelib: false\n", wheel_metadata)
            tags = [line.removeprefix("Tag: py3-none-") for line in wheel_metadata.splitlines() if line.startswith("Tag: ")]
            self.assertEqual(path.name, f"holon_types-{PYVERSION}-py3-none-{'.'.join(tags)}.whl")
            arch = "x86_64" if self.target.goarch == "amd64" else "aarch64"
            if self.target.goos == "linux":
                self.assertEqual(tags, [f"manylinux_2_17_{arch}", f"manylinux2014_{arch}", f"musllinux_1_2_{arch}"])
            else:
                self.assertEqual(len(tags), 1)
                arch = "arm64" if self.target.goarch == "arm64" else arch
                self.assertRegex(tags[0], rf"^macosx_\d+_\d+_{arch}$")
            records = list(csv.reader(io.StringIO(wheel.read(f"{info}/RECORD").decode())))
            self.assertEqual([row[0] for row in records], expected)
            self.assertEqual(records[-1], [f"{info}/RECORD", "", ""])
            for name, digest, size in records[:-1]:
                contents = wheel.read(name)
                self.assertEqual(int(size), len(contents))
                self.assertTrue(digest.startswith("sha256="))
                self.assertEqual(base64.urlsafe_b64decode(digest[7:] + "="), hashlib.sha256(contents).digest())

    def test_npm_packages(self):
        cpu = "x64" if self.target.goarch == "amd64" else "arm64"
        platform_name = f"holon-types-{self.target.goos}-{cpu}"
        for name, executable in ((platform_name, "bin/holon"), ("holon-types", "bin/holon.js")):
            with self.subTest(package=name), tarfile.open(self.dist / f"{name}-{VERSION}.tgz") as archive:
                self.assertEqual(archive.getnames(), ["package/package.json", f"package/{executable}",
                                                      "package/README.md", "package/LICENSE"])
                for entry in archive.getmembers():
                    self.assertEqual(entry.mode, 0o755 if entry.name == f"package/{executable}" else 0o644)
                    self.assertEqual(entry.mtime, 0)
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
        self.assertEqual(sorted(p.name for p in self.dist.iterdir()), sorted(p.name for p in second.iterdir()))
        for path in self.dist.iterdir():
            with self.subTest(package=path.name):
                self.assertEqual(path.read_bytes(), (second / path.name).read_bytes())

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
            for arch, cpu, wheel_arch in (("arm64", 0x0100000C, "arm64"), ("amd64", 0x01000007, "x86_64")):
                for command in (0x24, 0x32):
                    with self.subTest(arch=arch, command=command):
                        # Mach-O header and a deployment target of 12.3, independent of Go's defaults.
                        header = struct.pack("<8I", 0xFEEDFACF, cpu, 0, 2, 1, 24, 0, 0)
                        version = (12 << 16) | (3 << 8)
                        load = struct.pack("<6I", command, 24, 1, version, 0, 0) if command == 0x32 else struct.pack("<6I", command, 24, version, 0, 0, 0)
                        binary.write_bytes(header + load)
                        target = package_cli.Target("darwin", arch)
                        minimum = package_cli.verify_binary(binary, target)
                        self.assertEqual(minimum, (12, 3))
                        self.assertEqual(package_cli.wheel_tags(target, minimum), [f"macosx_12_3_{wheel_arch}"])
            binary.write_bytes(struct.pack("<8I", 0xFEEDFACF, 0x0100000C, 0, 2, 0, 0, 0, 0))
            with self.assertRaisesRegex(ValueError, "no minimum macOS version"):
                package_cli.verify_binary(binary, package_cli.Target("darwin", "arm64"))

    def test_only_static_linux_binaries_get_portable_tags(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "holon"
            for arch, machine, wheel_arch in (("amd64", 62, "x86_64"), ("arm64", 183, "aarch64")):
                for program_type in (1, 2, 3):
                    with self.subTest(arch=arch, program_type=program_type):
                        data = bytearray(120)
                        data[:6] = b"\x7fELF\x02\x01"
                        struct.pack_into("<H", data, 18, machine)
                        struct.pack_into("<Q", data, 32, 64)
                        struct.pack_into("<HH", data, 54, 56, 1)
                        struct.pack_into("<I", data, 64, program_type)
                        binary.write_bytes(data)
                        target = package_cli.Target("linux", arch)
                        if program_type == 1:
                            minimum = package_cli.verify_binary(binary, target)
                            self.assertEqual(package_cli.wheel_tags(target, minimum), [
                                f"manylinux_2_17_{wheel_arch}", f"manylinux2014_{wheel_arch}", f"musllinux_1_2_{wheel_arch}",
                            ])
                        else:
                            with self.assertRaisesRegex(ValueError, "must be static"):
                                package_cli.verify_binary(binary, target)

    def test_launcher_needs_neither_target_nor_go(self):
        with tempfile.TemporaryDirectory() as directory:
            subprocess.run([sys.executable, str(ROOT / "scripts/package_cli.py"), "--format", "npm-launcher",
                            "--out", directory, "--version", VERSION], env=os.environ | {"PATH": ""}, check=True)
            self.assertEqual([p.name for p in Path(directory).iterdir()], [f"holon-types-{VERSION}.tgz"])

    def test_binary_formats_require_a_target(self):
        for kind in ("tarball", "wheel", "npm-platform"):
            with self.subTest(kind=kind):
                result = subprocess.run([sys.executable, str(ROOT / "scripts/package_cli.py"), "--format", kind],
                                        text=True, capture_output=True)
                self.assertEqual(result.returncode, 2)
                self.assertIn("--target is required", result.stderr)


if __name__ == "__main__":
    unittest.main()
