"""Run the holon executable bundled in the holon-types wheel."""

import os
import sys
from pathlib import Path


def main() -> None:
    binary = os.environ.get("HOLON_BINARY") or str(Path(__file__).with_name("bin") / "holon")
    # Keep extraction in the environment that installed our SDK dependencies.
    os.environ["PATH"] = str(Path(sys.executable).parent) + os.pathsep + os.environ.get("PATH", os.defpath)
    try:
        os.execv(binary, [binary, *sys.argv[1:]])
    except OSError as error:
        print(f"holon: {binary}: {error}; reinstall holon-types", file=sys.stderr)
        sys.exit(1)
