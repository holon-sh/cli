package typegen

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/internal/testfiles"
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
		{"both", "keep exactly one server entry"},
		{"none", "has no server entry"},
		{"odd/index.ts", "is not a server entry"},
		{"missing", "no such file"},
	} {
		document, err := ResolveDocument(t.Context(), Source{Server: Path{Dir: filepath.Join(root, test.path)}}, Options{})
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

func TestGenerateKeepsFilesItDidNotWrite(t *testing.T) {
	for _, test := range []struct{ target, file string }{
		{TargetTS, "generated/holon-sh/index.mjs"},
		{TargetPy, "holon/__init__.py"},
	} {
		t.Run(test.file, func(t *testing.T) {
			dir := t.TempDir()
			runtimePackage(t, dir, "holon-sh")
			put(t, dir, "requirements.txt", "holon-sh\n")
			put(t, dir, test.file, "user content")
			c := Consumer{Name: "app", Dir: dir, Targets: []string{test.target}, Sources: []Source{{Server: document(t, ordersMetadata())}}}
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
	}{
		{"unrelated frontend", map[string]string{"package.json": `{"devDependencies":{"vite":"*"}}`, "server.py": ""}, nil},
		{"python with frontend", map[string]string{"package.json": `{}`, "pyproject.toml": `[project]
dependencies = ["holon-sh @ https://example.com/sdk.tar.gz"]`}, []string{TargetPy}},
		{"all", map[string]string{"package.json": `{"devDependencies":{"holon-sh":"*","holon-ui":"*"}}`, "requirements.txt": "holon-sh==1.0.0"}, []string{TargetTS, TargetUI, TargetPy}},
		{"go", map[string]string{"go.mod": "module app\nrequire (\n github.com/holon-sh/sdk-go v0.0.1\n)\n"}, []string{TargetGo}},
		{"poetry dev", map[string]string{"pyproject.toml": "[tool.poetry.dev-dependencies]\nholon_sh = \"*\"\n"}, []string{TargetPy}},
		{"pdm dev", map[string]string{"pyproject.toml": "[tool.pdm.dev-dependencies]\nlint = [\"holon-sh\"]\n"}, []string{TargetPy}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			testfiles.Write(t, dir, test.files)
			got, err := DeclaredTargets(dir)
			if !slices.Equal(got, test.want) || err != nil {
				t.Fatalf("targets = %v, %v; want %v", got, err, test.want)
			}
		})
	}
	dir := t.TempDir()
	put(t, dir, "pyproject.toml", "[project\n")
	if _, err := DeclaredTargets(dir); err == nil {
		t.Fatalf("malformed pyproject.toml: %v, want its parse error", err)
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
		out, err := AddPackageMember([]byte(c.in), "imports", "#holon-sh", "./generated/holon-sh/index.mjs")
		if err != nil || string(out) != c.out {
			t.Errorf("AddPackageMember(%q) = %q, %v; want %q", c.in, out, err, c.out)
		}
	}
}

func TestParseSource(t *testing.T) {
	cwd, _ := os.Getwd()
	for _, test := range []struct {
		arg  string
		want Source
	}{
		{arg: "orders=../orders/server.py", want: Source{Alias: "orders", Server: Path{Dir: filepath.Join(cwd, "../orders/server.py")}}},
		{arg: "https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4", want: Source{Server: Registry{Name: "io.github.upstash/context7", Version: "4.4.4", Registry: "https://registry.modelcontextprotocol.io"}}},
		{arg: "docs=https://registry.example/v0/servers/example.com%2Fdocs/versions/1.0.0", want: Source{Alias: "docs", Server: Registry{Name: "example.com/docs", Version: "1.0.0", Registry: "https://registry.example"}}},
		{arg: "https://huggingface.co/mcp", want: Source{Server: Endpoint("https://huggingface.co/mcp")}},
		{arg: "pets=./pets.yml", want: Source{Alias: "pets", Server: OpenAPI(filepath.Join(cwd, "pets.yml"))}},
		{arg: "https://api.example.test/openapi.json", want: Source{Server: Endpoint("https://api.example.test/openapi.json")}},
	} {
		got, err := ParseSource(test.arg)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("ParseSource(%q) = %+v, %v; want %+v", test.arg, got, err, test.want)
		}
	}
	for arg, want := range map[string]string{
		"orders=": "a server path or URL is required",
		"https://registry.example/v0/servers/example.com%2Forders/versions/latest": "exact",
		"https://user:secret@x.example/mcp":                                        "without credentials",
	} {
		if _, err := ParseSource(arg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseSource(%q) = %v, want %q", arg, err, want)
		}
	}
}
