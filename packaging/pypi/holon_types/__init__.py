"""Run the holon executable bundled in the holon-types wheel."""

import os
import sys
from pathlib import Path


def main() -> None:
    binary = os.environ.get("HOLON_BINARY") or str(Path(__file__).with_name("bin") / "holon")
    sys.stdout.flush()
    sys.stderr.flush()
    try:
        os.execv(binary, [binary, *sys.argv[1:]])
    except OSError as error:
        print(f"holon: {binary}: {error}; reinstall holon-types", file=sys.stderr)
        sys.exit(1)
