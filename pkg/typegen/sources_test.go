package typegen

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func TestServerEntryResolvesFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	put(t, root, "nested/src/server.ts", "export default {};\n")
	put(t, root, "flat/server.py", pythonProvider)
	put(t, root, "both/server.py", pythonProvider)
	put(t, root, "both/server.ts", "export default {};\n")
	put(t, root, "none/package.json", "{}")
	put(t, root, "odd/index.ts", "export default {};\n")
	for _, test := range []struct{ path, root, entry, language, want string }{
		{"nested/src/server.ts", "nested", "src/server.ts", "typescript", ""},
		{"nested", "nested", "src/server.ts", "typescript", ""},
		{"flat/server.py", "flat", "server.py", "python", ""},
		{"flat", "flat", "server.py", "python", ""},
		{"both", "", "", "", "keep exactly one server entry"},
		{"none", "", "", "", "has no server entry"},
		{"odd/index.ts", "", "", "", "is not a server entry"},
		{"missing", "", "", "", "no such file"},
	} {
		dir, entry, language, err := serverEntry(filepath.Join(root, test.path))
		if test.want != "" {
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("serverEntry(%s) = %v, want %q", test.path, err, test.want)
			}
			continue
		}
		if err != nil || dir != filepath.Join(root, test.root) || entry != test.entry || language != test.language {
			t.Errorf("serverEntry(%s) = %s %s %s %v", test.path, dir, entry, language, err)
		}
	}
}

func TestServerKey(t *testing.T) {
	for name, want := range map[string]string{
		"example.com/checkout":         "checkout",
		"io.github.Some_User/My.Svc":   "my-svc",
		"example.com/---":              "",
		"x/" + strings.Repeat("a", 60): strings.Repeat("a", 49),
	} {
		if got := serverKey(name); got != want {
			t.Errorf("serverKey(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestConsumerValidation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	put(t, dir, "file", "")
	ref := &Reference{Name: "example.com/orders", Version: "1.0.0"}
	for _, test := range []struct {
		name string
		c    Consumer
		want string
	}{
		{"ok", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Registry: ref}}}, ""},
		{"no name", Consumer{Dir: dir, Targets: []string{TargetTS}}, "name is required"},
		{"relative dir", Consumer{Name: "app", Dir: "relative", Targets: []string{TargetTS}}, "must be absolute"},
		{"not a dir", Consumer{Name: "app", Dir: file, Targets: []string{TargetTS}}, "is not a directory"},
		{"no target", Consumer{Name: "app", Dir: dir}, "at least one target"},
		{"bad target", Consumer{Name: "app", Dir: dir, Targets: []string{"node"}}, `unknown target "node"`},
		{"repeated target", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS, TargetTS}}, "listed twice"},
		{"server by endpoint", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Server: &Source{Endpoint: "https://x.example/mcp"}}, "must be a path source"},
		{"contract and endpoint", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Document: document(t, ordersMetadata()), Endpoint: "https://x.example/mcp"}}}, "exactly one of"},
		{"two kinds", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Path: dir, Endpoint: "https://x.example/mcp"}}}, "exactly one of"},
		{"bad alias", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Alias: "Bad_Alias", Registry: ref}}}, "must be a DNS label"},
		{"bad version", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Registry: &Reference{Name: "example.com/orders", Version: "latest"}}}}, "exact"},
	} {
		err := test.c.validate()
		if test.want == "" && err != nil {
			t.Errorf("%s: %v", test.name, err)
		}
		if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
			t.Errorf("%s: %v, want %q", test.name, err, test.want)
		}
	}
}

func slotOwner(name string) slotMetadata {
	return slotMetadata{Marker: mcpcontract.Marker, App: name, Identity: mcpcontract.Identity{Name: "example.com/" + name, Version: "1"}}
}

func TestNodeSlotResolvesSymlinkAndPreservesForeignContents(t *testing.T) {
	const pkg = "holon-sh"
	root := t.TempDir()
	store := filepath.Join(root, "store")
	app := filepath.Join(root, "app")
	put(t, store, "package.json", `{"holon":{"slot":"generated","contractVersion":1}}`)
	if err := os.MkdirAll(filepath.Join(app, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(store, filepath.Join(app, "node_modules", pkg)); err != nil {
		t.Fatal(err)
	}
	resolved, err := InstalledRuntime(app)
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(store)
	if resolved != real {
		t.Fatalf("slot = %s, want %s", resolved, real)
	}
	put(t, store, "generated/index.js", "user content")
	if err := writeNodeSlot(resolved, map[string][]byte{"index.js": []byte("generated")}, slotOwner("one")); err == nil {
		t.Fatal("overwrote foreign slot")
	}
	if got := read(t, store, "generated/index.js"); got != "user content" {
		t.Fatalf("foreign contents changed: %s", got)
	}
	put(t, store, "generated/holon-generated.json", string(mustJSON(t, slotMetadata{Marker: mcpcontract.Marker, Placeholder: true})))
	if err := writeNodeSlot(resolved, map[string][]byte{"index.js": []byte("generated")}, slotOwner("one")); err != nil {
		t.Fatal(err)
	}
	first, _ := os.Stat(filepath.Join(store, "generated/index.js"))
	if err := writeNodeSlot(resolved, map[string][]byte{"index.js": []byte("generated")}, slotOwner("one")); err != nil {
		t.Fatal(err)
	}
	second, _ := os.Stat(filepath.Join(store, "generated/index.js"))
	if !os.SameFile(first, second) {
		t.Fatal("unchanged generation replaced the slot")
	}
	// Older markers may carry digests; ownership still permits refreshing their slot.
	put(t, store, "generated/holon-generated.json", `{"marker":"holon-generated-apps-v1","app":"one","digests":{"orders":"sha256:old"}}`)
	if err := writeNodeSlot(resolved, map[string][]byte{"index.js": []byte("generated")}, slotOwner("one")); err != nil {
		t.Fatal(err)
	}
	if err := writeNodeSlot(resolved, map[string][]byte{"index.js": []byte("other")}, slotOwner("two")); err == nil || !strings.Contains(err.Error(), "install a separate "+pkg) {
		t.Fatalf("shared package was reassigned to another consumer: %v", err)
	}
}

func TestPythonSlotProtectsUserFiles(t *testing.T) {
	content := "# " + mcpcontract.Marker + " app=orders\napps = object()\n"
	files := map[string][]byte{"holon/apps/__init__.py": []byte(content)}

	root := t.TempDir()
	put(t, root, "holon/package.json", `{"holon":"holon-generated-apps-v1"}`)
	put(t, root, "holon/__init__.py", "legacy runtime")
	if err := writePythonSlot(root, files); err == nil {
		t.Fatal("legacy marker authorized runtime replacement")
	}
	if read(t, root, "holon/__init__.py") != "legacy runtime" {
		t.Fatal("legacy runtime changed")
	}

	for _, name := range []string{"holon.py", "holon/__init__.py", "holon/apps/__init__.py"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, name, "user content")
			if err := writePythonSlot(root, files); err == nil {
				t.Fatal("overwrote user namespace")
			}
			if got := read(t, root, name); got != "user content" {
				t.Fatal("user file changed")
			}
		})
	}
}

func TestDeclaredTargetsFollowSDKDependencies(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"unrelated frontend", map[string]string{"package.json": `{"devDependencies":{"vite":"*"}}`, "server.py": ""}, nil},
		{"node", map[string]string{"package.json": `{"dependencies":{"holon-sh":"*"}}`}, []string{TargetTS}},
		{"optional node", map[string]string{"package.json": `{"optionalDependencies":{"holon-sh":"*"}}`}, []string{TargetTS}},
		{"python with frontend", map[string]string{"package.json": `{}`, "pyproject.toml": `[project]
dependencies = ["holon-sh @ https://example.com/sdk.tar.gz"]`}, []string{TargetPy}},
		{"both", map[string]string{"package.json": `{"devDependencies":{"holon-sh":"*"}}`, "requirements.txt": "holon-sh==1.0.0"}, []string{TargetTS, TargetPy}},
		{"go", map[string]string{"go.mod": "module app\nrequire (\n github.com/holon-sh/sdk/go v0.0.1\n)\n"}, []string{TargetGo}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range test.files {
				put(t, dir, name, data)
			}
			got, err := DeclaredTargets(dir)
			if !slices.Equal(got, test.want) || (len(test.want) == 0 && !errors.Is(err, ErrNoRuntime)) || (len(test.want) != 0 && err != nil) {
				t.Fatalf("targets = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}
