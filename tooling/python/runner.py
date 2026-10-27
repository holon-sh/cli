"""Check an application against its extracted sources and typed contracts.

Usage: runner.py check|verify CONFIG. The config maps source paths to the
SHA-256 digests recorded at extraction and names the entry to type-check.
verify stops once the sources match; check then runs mypy from the app's
environment, which the holon-types wheel supplies.
"""

from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path

from mypy import api

mode, config = sys.argv[1:]
settings = json.loads(Path(config).read_text())
for path, digest in settings["sources"].items():
    with Path(path).open("rb") as source:
        actual = hashlib.file_digest(source, "sha256").hexdigest()
    if actual != digest:
        raise SystemExit(
            f"{path}: application sources changed after extraction; regenerate types before checking."
        )
if mode == "verify":
    sys.exit(0)
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
