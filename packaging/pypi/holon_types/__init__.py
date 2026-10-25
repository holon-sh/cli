"""Launch the platform binary bundled in the holon-types wheel."""

import os
import sys
from pathlib import Path


def main() -> None:
    binary = Path(__file__).with_name("bin") / "holon"
    os.execv(binary, [str(binary), *sys.argv[1:]])
