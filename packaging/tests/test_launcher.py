import os
import shutil
import signal
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class LauncherTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix="holon launcher ")
        self.addCleanup(directory.cleanup)
        self.directory = Path(directory.name).resolve()
        self.binary = self.directory / "fake holon"
        shutil.copyfile(Path(__file__).with_name("fake-holon.sh"), self.binary)
        self.binary.chmod(0o755)
        node = Path(shutil.which("node")).resolve()
        self.launchers = [
            ([sys.executable, "-c", "import holon_types; holon_types.main()"], "/usr/bin:/bin", "holon_types/bin/holon"),
            ([str(node), str(ROOT / "packaging/npm/launcher/bin/holon.js")], f"{node.parent}:/usr/bin:/bin", r"holon-types-(darwin|linux)-(arm64|x64)"),
        ]

    def launch(self, command, args=(), **env):
        environment = os.environ | {
            "PYTHONPATH": str(ROOT / "packaging/pypi"), "HOLON_BINARY": str(self.binary),
            "FROM_ENV": "inherited value", "VIRTUAL_ENV": "/project/.venv",
            "PATH": "/usr/bin:/bin", "EXIT_WITH": "0", "DIE_WITH": "",
        } | env
        return subprocess.run(
            [*command, *args], cwd=self.directory,
            env={key: value for key, value in environment.items() if value is not None},
            input="input from caller\n", text=True, capture_output=True, timeout=10,
        )

    def test_process_contract(self):
        for command, path, _ in self.launchers:
            for status in (0, 17):
                with self.subTest(launcher=command[0], status=status):
                    result = self.launch(command, ["--version", "-s", "a=b", "", "two words"], EXIT_WITH=str(status))
                    self.assertEqual(result.returncode, status)
                    self.assertEqual(result.stdout, "<--version>\n<-s>\n<a=b>\n<>\n<two words>\n"
                                     f"env=inherited value\ncwd={self.directory}\npath={path}\n"
                                     "venv=/project/.venv\nstdin=input from caller\n")
                    self.assertEqual(result.stderr, "child stderr\n")

    def test_signal(self):
        for command, _, _ in self.launchers:
            with self.subTest(launcher=command[0]):
                self.assertEqual(self.launch(command, DIE_WITH="TERM").returncode, -signal.SIGTERM)

    def test_unusable_binary(self):
        self.binary.chmod(0o644)
        for command, _, _ in self.launchers:
            for binary in (self.directory / "missing", self.binary):
                with self.subTest(launcher=command[0], binary=binary):
                    result = self.launch(command, HOLON_BINARY=str(binary))
                    self.assertEqual(result.returncode, 1)
                    self.assertEqual(result.stdout, "")
                    self.assertRegex(result.stderr, r"^holon: ")
                    self.assertNotIn("Traceback", result.stderr)

    def test_missing_bundled_binary(self):
        for command, _, missing in self.launchers:
            for override in ("", None):
                with self.subTest(launcher=command[0], override=override):
                    result = self.launch(command, HOLON_BINARY=override)
                    self.assertEqual(result.returncode, 1)
                    self.assertRegex(result.stderr, missing)
                    self.assertIn("reinstall holon-types", result.stderr)
