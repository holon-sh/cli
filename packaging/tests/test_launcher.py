import os
import shutil
import signal
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from scripts.package_cli import python_version

ROOT = Path(__file__).resolve().parents[2]


class VersionTests(unittest.TestCase):
    def test_python_versions(self):
        for version, expected in {
            "1.2.3": "1.2.3", "1.2.3-rc.1": "1.2.3rc1", "1.2.3-alpha.2": "1.2.3a2",
            "1.2.3-beta.1": "1.2.3b1", "1.2.3-dev.4": "1.2.3.dev4",
        }.items():
            with self.subTest(version=version):
                self.assertEqual(python_version(version), expected)


class LauncherTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix="holon launcher ")
        self.addCleanup(directory.cleanup)
        self.directory = Path(directory.name).resolve()
        self.binary = self.directory / "fake holon"
        shutil.copyfile(Path(__file__).with_name("fake-holon.sh"), self.binary)
        self.binary.chmod(0o755)

    def launch(self, args=(), **env):
        return subprocess.run(
            [sys.executable, "-c", "import holon_types; holon_types.main()", *args],
            cwd=self.directory,
            env=os.environ | {
                "PYTHONPATH": str(ROOT / "packaging/pypi"),
                "HOLON_BINARY": str(self.binary),
                "FROM_ENV": "inherited value",
                "VIRTUAL_ENV": "/project/.venv",
                "PATH": "/usr/bin:/bin",
                "EXIT_WITH": "0",
                "DIE_WITH": "",
            } | env,
            input="input from caller\n", text=True, capture_output=True, timeout=10,
        )

    def test_process_contract(self):
        for status in (0, 3, 17):
            with self.subTest(status=status):
                result = self.launch(["--version", "-s", "a=b", "", "two words"], EXIT_WITH=str(status))
                self.assertEqual(result.returncode, status)
                self.assertEqual(result.stdout, "<--version>\n<-s>\n<a=b>\n<>\n<two words>\n"
                                 f"env=inherited value\ncwd={self.directory}\npath=/usr/bin:/bin\n"
                                 "venv=/project/.venv\nstdin=input from caller\n")
                self.assertEqual(result.stderr, "child stderr\n")

    def test_signal(self):
        self.assertEqual(self.launch(DIE_WITH="TERM").returncode, -signal.SIGTERM)

    def test_unusable_binary(self):
        for binary in (self.directory / "missing", self.binary, self.directory):
            with self.subTest(binary=binary):
                self.binary.chmod(0o644)
                result = self.launch(HOLON_BINARY=str(binary))
                self.assertEqual(result.returncode, 1)
                self.assertEqual(result.stdout, "")
                self.assertTrue(result.stderr.startswith("holon: "), result.stderr)
                self.assertNotIn("Traceback", result.stderr)

    def test_empty_override_uses_bundled_path(self):
        result = self.launch(HOLON_BINARY="")
        self.assertEqual(result.returncode, 1)
        self.assertIn("holon_types/bin/holon", result.stderr)
        self.assertIn("reinstall holon-types", result.stderr)


if __name__ == "__main__":
    unittest.main()
