// Package python embeds the Python contract runner that pkg/tooling unpacks.
package python

import "embed"

// Files holds the runner and the dependencies it needs, as listed in pyproject.toml.
//
//go:embed extract.py runner.py requirements.txt
var Files embed.FS
