"""Type-check an application against its typed contracts.

Usage: runner.py check CONFIG. The config names the entry to type-check. mypy must be importable by
the app's interpreter: the holon-types wheel installs it on the host, and an image check puts
/opt/holon-check on PYTHONPATH.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

from mypy import api

_, config = sys.argv[1:]
settings = json.loads(Path(config).read_text())
stdout, stderr, status = api.run(
    [
        "--no-incremental",
        "--check-untyped-defs",
        "--explicit-package-bases",
        "--exclude",
        r"/(\.venv|venv|node_modules)/",
        settings["entry"] or ".",
    ]
)
sys.stdout.write(stdout)
sys.stderr.write(stderr)
sys.exit(status)
