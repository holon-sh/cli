import os
import platform
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import package_cli  # noqa: E402


class CreateTests(unittest.TestCase):
    """Pack a release around the fake holon, then start an app the way npm and uvx users do."""

    def test_create_app(self):
        directory = Path(self.enterContext(tempfile.TemporaryDirectory(prefix="holon create "))).resolve()
        binary, dist = directory / "holon", directory / "dist"
        shutil.copyfile(Path(__file__).with_name("fake-holon.sh"), binary)
        dist.mkdir()
        arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine(), platform.machine())
        package_cli.write_npm_platform(binary, dist, "1.2.3", package_cli.Target(sys.platform, arch))
        package_cli.write_npm_launcher(dist, "1.2.3")
        package_cli.write_npm_create(dist, "1.2.3")
        package_cli.write_wheel(binary, dist, "1.2.3", ["any"])

        def run(*command):
            env = os.environ | {"npm_config_offline": "true", "UV_OFFLINE": "1"}
            return subprocess.run(command, cwd=directory, env=env, input="", text=True, capture_output=True, check=True).stdout

        run("npm", "install", "--no-audit", "--no-fund", *map(str, dist.glob("*.tgz")))
        run("uv", "venv", "--python", sys.executable, ".venv")
        run("uv", "pip", "install", "--python", ".venv", "--no-deps", *map(str, dist.glob("*.whl")))
        for command, lang in ((["npm", "create", "holon-app", "orders", "--", "--lang", "ts"], "ts"),
                              ([".venv/bin/holon-types", "init", "orders", "--lang", "py"], "py")):
            with self.subTest(command=command[0]):
                args = [line for line in run(*command).splitlines() if line.startswith("<")]
                self.assertEqual(args, ["<init>", "<orders>", "<--lang>", f"<{lang}>"])
