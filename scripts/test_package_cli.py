import hashlib
import json
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile
from pathlib import Path

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
            with zipfile.ZipFile(next(output.glob("holon_types-*.whl"))) as wheel:
                metadata = next(name for name in wheel.namelist() if name.endswith(".dist-info/METADATA"))
                self.assertIn("Requires-Dist: mypy<3,>=2.4", wheel.read(metadata).decode())
                self.assertIn("Requires-Dist: mcp<3,>=2.3", wheel.read(metadata).decode())
                self.assertIn("Requires-Dist: pydantic<3,>=2.14", wheel.read(metadata).decode())
