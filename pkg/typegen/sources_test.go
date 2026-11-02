package typegen

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolveDocumentFindsServerSources(t *testing.T) {
	needPython(t)
	root := t.TempDir()
	writeProvider(t, filepath.Join(root, "nested"), "orders", pythonProvider)
	if err := os.Mkdir(filepath.Join(root, "nested/src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "nested/server.py"), filepath.Join(root, "nested/src/server.py")); err != nil {
		t.Fatal(err)
	}
	put(t, root, "both/server.py", pythonProvider)
	put(t, root, "both/server.ts", "export default {};\n")
	put(t, root, "none/package.json", "{}")
	put(t, root, "odd/index.ts", "export default {};\n")
	for _, test := range []struct{ path, want string }{
		{"nested/src/server.py", ""},
		{"nested", ""},
		{"both", "keep exactly one server entry"},
		{"none", "has no server entry"},
		{"odd/index.ts", "is not a server entry"},
		{"missing", "no such file"},
	} {
		document, err := ResolveDocument(t.Context(), Source{Path: filepath.Join(root, test.path)}, Options{})
		if test.want != "" {
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("%s: %v, want %q", test.path, err, test.want)
			}
			continue
		}
		if err != nil || document.Schema == nil || len(document.Schema.Tools) != 1 || document.Schema.Tools[0].Name != "read" {
			t.Errorf("%s: contract = %+v, %v", test.path, document, err)
		}
	}
}

func TestPrepareRejectsInvalidConsumers(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	put(t, dir, "file", "")
	ref := &Reference{Name: "example.com/orders", Version: "1.0.0"}
	for _, test := range []struct {
		name string
		c    Consumer
		want string
	}{
		{"no name", Consumer{Dir: dir, Targets: []string{TargetTS}}, "name is required"},
		{"relative dir", Consumer{Name: "app", Dir: "relative", Targets: []string{TargetTS}}, "must be absolute"},
		{"not a dir", Consumer{Name: "app", Dir: file, Targets: []string{TargetTS}}, "is not a directory"},
		{"no target", Consumer{Name: "app", Dir: dir}, "at least one target"},
		{"bad target", Consumer{Name: "app", Dir: dir, Targets: []string{"node"}}, `unknown target "node"`},
		{"repeated target", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS, TargetTS}}, "listed twice"},
		{"server by endpoint", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Server: &Source{Endpoint: "https://x.example/mcp"}}, "must be a path source"},
		{"contract and endpoint", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Document: document(t, ordersMetadata()), Endpoint: "https://x.example/mcp"}}}, "exactly one of"},
		{"bad alias", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Alias: "Bad_Alias", Registry: ref}}}, "must be a DNS label"},
		{"bad version", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Registry: &Reference{Name: "example.com/orders", Version: "latest"}}}}, "exact"},
	} {
		prepared, err := Prepare(t.Context(), []Consumer{test.c}, Options{})
		if prepared != nil {
			prepared.Close()
		}
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: %v, want %q", test.name, err, test.want)
		}
	}
}

func TestGenerateKeepsFilesItDidNotWrite(t *testing.T) {
	for _, test := range []struct{ target, file string }{
		{TargetTS, "node_modules/holon-sh/generated/index.js"},
		{TargetPy, "holon.py"},
		{TargetPy, "holon/__init__.py"},
		{TargetPy, "holon/apps/__init__.py"},
		{TargetGo, "holon/mine.go"},
	} {
		t.Run(test.file, func(t *testing.T) {
			dir := t.TempDir()
			put(t, dir, "node_modules/holon-sh/package.json", `{"holon":{"slot":"generated","contractVersion":1}}`)
			put(t, dir, "requirements.txt", "holon-sh\n")
			put(t, dir, test.file, "user content")
			c := Consumer{Name: "app", Dir: dir, Targets: []string{test.target}, Sources: []Source{{Document: document(t, ordersMetadata())}}}
			if err := Generate(t.Context(), []Consumer{c}, Options{}); err == nil {
				t.Fatal("generated over a file it did not write")
			}
			if got := read(t, dir, test.file); got != "user content" {
				t.Fatalf("user file changed: %s", got)
			}
		})
	}
}

func TestDeclaredTargetsFollowSDKDependencies(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string]string
		want  []string
		err   error
	}{
		{"unrelated frontend", map[string]string{"package.json": `{"devDependencies":{"vite":"*"}}`, "server.py": ""}, nil, ErrNoRuntime},
		{"optional node", map[string]string{"package.json": `{"optionalDependencies":{"holon-sh":"*"}}`}, []string{TargetTS}, nil},
		{"python with frontend", map[string]string{"package.json": `{}`, "pyproject.toml": `[project]
dependencies = ["holon-sh @ https://example.com/sdk.tar.gz"]`}, []string{TargetPy}, nil},
		{"all", map[string]string{"package.json": `{"devDependencies":{"holon-sh":"*","holon-ui":"*"}}`, "requirements.txt": "holon-sh==1.0.0"}, []string{TargetTS, TargetUI, TargetPy}, nil},
		{"go", map[string]string{"go.mod": "module app\nrequire (\n github.com/holon-sh/sdk/go v0.0.1\n)\n"}, []string{TargetGo}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range test.files {
				put(t, dir, name, data)
			}
			got, err := DeclaredTargets(dir)
			if !slices.Equal(got, test.want) || !errors.Is(err, test.err) {
				t.Fatalf("targets = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}
