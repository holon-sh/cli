"""Create a Python app with the holon executable from holon-types."""

import sys

import holon_types


def main() -> None:
    sys.argv[1:1] = ["init", "--lang", "py"]
    holon_types.main()
