"""Write the deterministic Python tooling archive that pkg/tooling embeds."""

from __future__ import annotations

import re
import zipfile
from pathlib import Path


def build(root: Path) -> Path:
    match = re.search(
        r'(?m)^version = "([^"]+)"', (root / "pyproject.toml").read_text()
    )
    if match is None:
        raise SystemExit("pyproject.toml has no version")
    destination = root.parents[1] / "pkg/tooling"
    destination.mkdir(parents=True, exist_ok=True)
    for old in destination.glob("cli-tooling-py-*.zip"):
        old.unlink()
    archive = destination / f"cli-tooling-py-{match[1]}.zip"
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as bundle:
        for name in ("extract.py", "runner.py"):
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o100644 << 16
            bundle.writestr(info, (root / name).read_bytes())
    return archive


if __name__ == "__main__":
    print(build(Path(__file__).resolve().parent))
