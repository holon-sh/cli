"""Type-check an application against its typed contracts.

Usage: runner.py check ENTRY, in the application's directory; an empty ENTRY checks all of it. mypy
must be importable by the app's interpreter: the holon-types wheel installs it on the host, and an
image check puts /opt/holon-check on PYTHONPATH.
"""

from __future__ import annotations

import sys

from mypy import api

stdout, stderr, status = api.run(
    [
        "--no-incremental",
        "--check-untyped-defs",
        "--explicit-package-bases",
        "--exclude",
        r"/(\.venv|venv|node_modules)/",
        sys.argv[2] or ".",
    ]
)
sys.stdout.write(stdout)
sys.stderr.write(stderr)
sys.exit(status)
