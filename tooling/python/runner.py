from __future__ import annotations

import hashlib
import json
import sys
from collections.abc import Callable
from pathlib import Path
from typing import Any

# extract.py sits beside this file, both in the source tree and in the tooling bundle.
from extract import extract


def check(config: dict[str, Any], verify_only: bool) -> None:
    for path, expected in config["sources"].items():
        if hashlib.sha256(Path(path).read_bytes()).hexdigest() != expected:
            raise ValueError(
                f"{path}: application sources changed after extraction; regenerate types before checking."
            )
    if verify_only or not config["check"]:
        return
    # The app's environment supplies mypy through the holon-types wheel.
    from mypy import api

    stdout, stderr, status = api.run(
        [
            "--no-incremental",
            "--check-untyped-defs",
            "--explicit-package-bases",
            "--exclude",
            r"/(\.venv|venv|node_modules)/",
            config["entry"] or ".",
        ]
    )
    sys.stdout.write(stdout)
    sys.stderr.write(stderr)
    if status:
        raise SystemExit(status)


MODES: dict[str, Callable[[dict[str, Any]], object]] = {
    "extract": lambda config: {
        app["name"]: extract(app["path"], app["entry"]) for app in config["apps"]
    },
    "check": lambda config: check(config, False),
    "verify": lambda config: check(config, True),
}


def main() -> None:
    mode, path, *output = sys.argv[1:]
    result = MODES[mode](json.loads(Path(path).read_text()))
    if output:
        Path(output[0]).write_text(json.dumps(result))


if __name__ == "__main__":
    main()
