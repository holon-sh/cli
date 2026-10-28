package typegen

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeDescriptor(t *testing.T) {
	for _, test := range []struct{ entry, source, versionFile, version, dynamic string }{
		{"server.ts", `import {McpServer, createMcpHandler} from "@modelcontextprotocol/server";
import {version} from "./version.ts";
import {writeFileSync} from "node:fs";
writeFileSync(new URL("./executed",import.meta.url), "unexpected");
const info = {name:"example.com/orders",version,description:"Order tools"};
export const publishing = {packages:[{registryType:"oci",identifier:` + "`images.example/orders:${info.version}`" + `,transport:{type:"stdio"}}]};
export default createMcpHandler(()=>new McpServer(info));`, "version.ts", `export const version = "1.2.3";`, `import {McpServer,createMcpHandler} from "@modelcontextprotocol/server"; export default createMcpHandler(()=>new McpServer(getInfo()));`},
		{"server.py", `from mcp.server import MCPServer
from version import version
from pathlib import Path
Path(__file__).with_name("executed").write_text("unexpected")
server = MCPServer("example.com/orders", version=version, description="Order tools")
publishing = {"packages": [{"registryType":"oci", "identifier": f"images.example/orders:{version}", "transport":{"type":"stdio"}}]}
`, "version.py", `version = "1.2.3"`, `from mcp.server import MCPServer
server = MCPServer("example.com/orders", version="1", description="Orders", **getInfo())`},
	} {
		t.Run(test.entry, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, test.entry, test.source)
			put(t, root, test.versionFile, test.version)
			for _, version := range []string{"1.2.3", "2.0.0"} {
				put(t, root, test.versionFile, strings.ReplaceAll(test.version, "1.2.3", version))
				raw, err := ReadDescriptor(t.Context(), root, Options{})
				if err != nil {
					t.Fatal(err)
				}
				d, err := ParseDescriptor(raw)
				if err != nil {
					t.Fatal(err)
				}
				if d.Name != "example.com/orders" || d.Description != "Order tools" || d.Version != version || d.Packages[0].Identifier != "images.example/orders:"+version {
					t.Fatalf("descriptor = %s", raw)
				}
				document, err := ResolveDocument(t.Context(), Source{Path: root}, Options{})
				if err != nil {
					t.Fatal(err)
				}
				if document.Identity.Version != version {
					t.Fatalf("contract identity = %+v", document.Identity)
				}
			}
			for _, file := range []string{"server.json", "executed"} {
				if _, err := os.Stat(filepath.Join(root, file)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("source modified: %s (%v)", file, err)
				}
			}
			put(t, root, test.entry, test.dynamic)
			if _, err := ReadDescriptor(t.Context(), root, Options{}); err == nil {
				t.Fatal("dynamic constructor metadata was accepted")
			}
			// Authored JSON remains authoritative, even when implementation metadata cannot be extracted.
			put(t, root, test.entry, "invalid source")
			put(t, root, "server.json", `{"name":"example.com/authored","version":"3","description":"Authored"}`)
			raw, err := ReadDescriptor(t.Context(), root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			var d map[string]any
			if err := json.Unmarshal(raw, &d); err != nil {
				t.Fatal(err)
			}
			if d["name"] != "example.com/authored" {
				t.Fatalf("authored descriptor ignored: %s", raw)
			}
			put(t, root, test.entry, test.source)
			put(t, root, "server.json", "invalid JSON")
			if _, err := ReadDescriptor(t.Context(), root, Options{}); err == nil {
				t.Fatal("invalid authored descriptor fell back to code")
			}
		})
	}
	root := t.TempDir()
	if raw, err := ReadDescriptor(t.Context(), root, Options{}); err != nil || raw != nil {
		t.Fatalf("non-server = %s, %v", raw, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadDescriptor(ctx, root, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}
