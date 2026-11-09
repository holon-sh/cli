package typegen

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/protocol"
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
	ref := &protocol.RegistryRef{Name: "example.com/orders", Version: "1.0.0"}
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
		{"bad version", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Registry: &protocol.RegistryRef{Name: "example.com/orders", Version: "latest"}}}}, "exact"},
		{"bad endpoint", Consumer{Name: "app", Dir: dir, Targets: []string{TargetTS}, Sources: []Source{{Endpoint: "https://user:secret@x.example/mcp"}}}, "without credentials"},
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
		{TargetTS, "generated/holon-sh/index.mjs"},
		{TargetPy, "holon.py"},
		{TargetPy, "holon/__init__.py"},
		{TargetPy, "holon/apps/__init__.py"},
		{TargetGo, "holon/mine.go"},
	} {
		t.Run(test.file, func(t *testing.T) {
			dir := t.TempDir()
			runtimePackage(t, dir, "holon-sh")
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
		{"go", map[string]string{"go.mod": "module app\nrequire (\n github.com/holon-sh/sdk-go v0.0.1\n)\n"}, []string{TargetGo}, nil},
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

// Install maps a project's clients in package.json as a package manager edits it: one member added,
// in the file's layout, and a mapping the project already has kept.
func TestAddImportKeepsPackageJSONAsWritten(t *testing.T) {
	const entry = `"#holon-sh": "./generated/holon-sh/index.mjs"`
	for _, c := range []struct{ in, out string }{
		{"{\n  \"name\": \"a\",\n  \"files\": [\"dist\"]\n}\n", "{\n  \"name\": \"a\",\n  \"files\": [\"dist\"],\n  \"imports\": {\n    " + entry + "\n  }\n}\n"},
		{"{\r\n\t\"imports\": {\r\n\t\t\"#lib\": \"./lib.js\"\r\n\t}\r\n}", "{\r\n\t\"imports\": {\r\n\t\t\"#lib\": \"./lib.js\",\r\n\t\t" + entry + "\r\n\t}\r\n}"},
		{`{"imports":{"#holon-sh":{"browser":"./x.mjs"}}}`, ""},
	} {
		out, err := addImport([]byte(c.in), "#holon-sh", "./generated/holon-sh/index.mjs")
		if err != nil || string(out) != c.out {
			t.Errorf("addImport(%q) = %q, %v; want %q", c.in, out, err, c.out)
		}
	}
}

func TestParseSource(t *testing.T) {
	cwd, _ := os.Getwd()
	for _, test := range []struct {
		arg  string
		want Source
	}{
		{arg: "../orders", want: Source{Path: filepath.Join(cwd, "../orders")}},
		{arg: "orders=../orders/server.py", want: Source{Alias: "orders", Path: filepath.Join(cwd, "../orders/server.py")}},
		{arg: "https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4", want: Source{Registry: &protocol.RegistryRef{Name: "io.github.upstash/context7", Version: "4.4.4", Registry: "https://registry.modelcontextprotocol.io"}}},
		{arg: "docs=https://registry.example/v0/servers/example.com%2Fdocs/versions/1.0.0", want: Source{Alias: "docs", Registry: &protocol.RegistryRef{Name: "example.com/docs", Version: "1.0.0", Registry: "https://registry.example"}}},
		{arg: "https://mcp.context7.com/mcp", want: Source{Endpoint: "https://mcp.context7.com/mcp"}},
	} {
		got, err := ParseSource(test.arg)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("ParseSource(%q) = %+v, %v; want %+v", test.arg, got, err, test.want)
		}
	}
	if _, err := ParseSource("orders="); err == nil {
		t.Error("ParseSource accepted an alias without a server")
	}
}
