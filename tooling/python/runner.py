"""AST worker and compatibility entrypoint for the shared Go tooling core."""

import json
import os
import platform
import sys
from pathlib import Path

from extract import extract

if __name__ == "__main__":
    mode, *args = sys.argv[1:]
    if mode == "analyze":
        print(json.dumps(extract(*args)))
    else:
        arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(
            platform.machine(), platform.machine()
        )
        core = Path(__file__).resolve().parents[1] / "core" / f"{sys.platform}-{arch}"
        os.execv(core, [str(core), "python", sys.executable, __file__, mode, *args])
