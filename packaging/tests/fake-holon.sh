#!/bin/sh
if [ -n "$DIE_WITH" ]; then kill -s "$DIE_WITH" "$$"; fi
for arg in "$@"; do printf '<%s>\n' "$arg"; done
printf 'env=%s\ncwd=%s\npath=%s\nvenv=%s\n' "$FROM_ENV" "$PWD" "$PATH" "$VIRTUAL_ENV"
IFS= read -r input
printf 'stdin=%s\n' "$input"
printf 'child stderr\n' >&2
exit "${EXIT_WITH:-0}"
