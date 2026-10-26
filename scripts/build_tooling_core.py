"""Build the reproducible compatibility host for every supported CLI platform."""

import os
import tempfile
import zipfile
from pathlib import Path

from package_cli import ROOT, build_binary, verify_binary
from release_common import TARGETS

# Pin the compiler as well as dependencies: its version changes the binary bytes.
os.environ["GOTOOLCHAIN"] = "go1.27.2"
os.environ["GOWORK"] = "off"
with tempfile.TemporaryDirectory() as temporary:
    with zipfile.ZipFile(ROOT / "pkg/tooling/cli-tooling-core.zip", "w") as archive:
        for target in TARGETS:
            output = Path(temporary) / f"{target.goos}-{target.goarch}"
            build_binary(output, target, "", "./cmd/holon-tooling")
            verify_binary(output, target)
            info = zipfile.ZipInfo(output.name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o100755 << 16
            archive.writestr(info, output.read_bytes())
