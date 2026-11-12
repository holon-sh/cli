// Package python embeds the Python contract runner that pkg/tooling unpacks.
package python

import "embed"

// Files holds the contract extractor and runner.
//
//go:embed extract.py runner.py
var Files embed.FS
