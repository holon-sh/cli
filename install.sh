#!/bin/sh
# Compatibility entry point; the installer lives in holon-sh/website.
#
#   curl -fsSL https://holon-labs.dev/install.sh | sh
set -eu

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
curl -fsSL https://holon-labs.dev/install.sh -o "$tmp"
sh "$tmp" "$@"
