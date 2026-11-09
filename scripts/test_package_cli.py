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
                    "--format", "tarball", "--format", "wheel", "--format", "npm-platform", "--format", "launcher",
                ], check=True, capture_output=True)
                packages.append({p.name: hashlib.sha256(p.read_bytes()).digest() for p in output.iterdir()})
            self.assertEqual(len(packages[0]), 6)
            self.assertEqual(packages[0], packages[1])
            with tarfile.open(output / "holon-types-1.2.3-rc.1.tgz") as archive:
                launcher = json.load(archive.extractfile("package/package.json"))
            self.assertEqual(launcher["optionalDependencies"], {name: "1.2.3-rc.1" for name in (
                "holon-types-darwin-arm64", "holon-types-darwin-x64", "holon-types-linux-arm64", "holon-types-linux-x64",
            )})
            with tarfile.open(output / "create-holon-app-1.2.3-rc.1.tgz") as archive:
                create = json.load(archive.extractfile("package/package.json"))
            self.assertEqual(create["dependencies"], {"holon-types": "1.2.3-rc.1"})
            with zipfile.ZipFile(output / "create_holon_app-1.2.3rc1-py3-none-any.whl") as archive:
                metadata = archive.read("create_holon_app-1.2.3rc1.dist-info/METADATA").decode()
            self.assertIn("Requires-Dist: holon-types==1.2.3-rc.1\n", metadata)
            for package in output.iterdir():
                with (zipfile.ZipFile if package.suffix == ".whl" else tarfile.open)(package) as archive:
                    names = archive.namelist() if package.suffix == ".whl" else archive.getnames()
                self.assertTrue({"LICENSE", "NOTICE", "THIRD-PARTY-NOTICES"} <= {Path(name).name for name in names}, package.name)
