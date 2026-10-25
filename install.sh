#!/bin/sh
# Installs the holon CLI from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/holon-sh/cli/main/install.sh | sh
#
# HOLON_VERSION      version to install (default: the newest release)
# HOLON_INSTALL_DIR  directory for the binary (default: ~/.local/bin)
set -eu

repo="holon-sh/cli"
base="${HOLON_RELEASE_URL:-https://github.com/$repo/releases/download}"
dir="${HOLON_INSTALL_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "holon: unsupported operating system $(uname -s); macOS and Linux are supported" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "holon: unsupported architecture $(uname -m); arm64 and x86-64 are supported" >&2; exit 1 ;;
esac

version="${HOLON_VERSION:-}"
if [ -z "$version" ]; then
  version="$(curl -fsSL "https://api.github.com/repos/$repo/releases?per_page=1" | sed -n 's/^[[:space:]]*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n 1)"
  [ -n "$version" ] || { echo "holon: no release found at https://github.com/$repo/releases" >&2; exit 1; }
fi

archive="holon_${version}_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$archive" "$base/v$version/$archive"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/v$version/SHA256SUMS"

expected="$(grep " $archive\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | cut -d' ' -f1)"
else
  actual="$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)"
fi
[ -n "$expected" ] && [ "$expected" = "$actual" ] || { echo "holon: checksum mismatch for $archive" >&2; exit 1; }

mkdir -p "$dir"
tar -xzf "$tmp/$archive" -C "$tmp" holon
mv "$tmp/holon" "$dir/holon"
chmod 755 "$dir/holon"

echo "Installed holon $version to $dir/holon"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Add $dir to your PATH, for example: export PATH=\"$dir:\$PATH\"" ;;
esac
