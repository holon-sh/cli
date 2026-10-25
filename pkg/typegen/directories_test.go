package typegen

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// appSummary renders a manifest's apps as "name:path" or "name:registry" with an access marker.
func appSummary(m Manifest) []string {
	var out []string
	for _, app := range m.Apps {
		entry := app.Name + ":path=" + app.Path
		if app.Registry != nil {
			entry = app.Name + ":registry=" + app.Registry.Name + "@" + app.Registry.Version
		}
		if app.Access != nil {
			entry += "+server"
		}
		out = append(out, entry)
	}
	return out
}

func pythonModule(t *testing.T, prepared *Prepared, name string) string {
	t.Helper()
	for _, app := range prepared.Manifest.Apps {
		if app.Name == name && app.prepared != nil {
			return app.prepared.packages["python"]["holon/apps/__init__.py"]
		}
	}
	t.Fatalf("%s was not prepared", name)
	return ""
}

func fileMap(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err == nil {
			files[relative] = read(t, root, relative)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestDirectoriesResolveRequiresThroughTheRegistry(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/v0.1/servers/example.com%2Forders/versions/1.0.0":
			w.Write(mustJSON(t, map[string]any{"server": map[string]any{
				"name": "example.com/orders", "version": "1.0.0", "description": "Orders",
				"_meta": map[string]any{PublisherMetadata: map[string]any{"sh.holon": map[string]any{
					"types":    server.URL + "/orders.json",
					"requires": map[string]Reference{"inventory": {Name: "example.com/inventory", Version: "1.0.0"}},
				}}},
			}}))
		case "/orders.json":
			w.Write(mustJSON(t, ordersMetadata()))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := filepath.Join(t.TempDir(), "checkout-src")
	writeProvider(t, dir, "checkout", "from holon.apps import apps\n"+pythonProvider, map[string]Reference{
		"orders": {Name: "example.com/orders", Version: "1.0.0", Registry: server.URL},
	})
	m, names, err := Directories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"checkout"}) {
		t.Fatalf("names = %v, want [checkout]", names)
	}
	if got, want := strings.Join(appSummary(m), " "), "checkout:path="+dir+"+server orders:registry=example.com/orders@1.0.0+server"; got != want {
		t.Fatalf("apps = %s, want %s", got, want)
	}
	var calls []string
	results := map[string]extraction{"checkout": {UsesApps: true}}
	prepared, err := Prepare(t.Context(), m, Options{Apps: names, HTTPClient: server.Client(), Extract: fakeExtract(t, results, &calls)})
	if err != nil {
		t.Fatalf("a registry dependency's own requires blocked generation: %v", err)
	}
	defer prepared.Close()
	if expected := prepared.Metadata()["checkout"].Requires["orders"]; len(expected.Contract.Tools) != 1 || expected.Contract.Tools[0].Name != "lookup" {
		t.Fatalf("orders expectation = %+v", expected)
	}
	if module := pythonModule(t, prepared, "checkout"); !strings.Contains(module, "async def lookup(") {
		t.Fatalf("checkout client is not typed from the registry contract:\n%s", module)
	}

	t.Run("platform manifest still expands every app", func(t *testing.T) {
		platform := m
		platform.pathRequiresOnly = false
		_, err := Prepare(t.Context(), platform, Options{Apps: names, HTTPClient: server.Client(), Extract: fakeExtract(t, results, &calls)})
		if err == nil || !strings.Contains(err.Error(), "must be declared in apps") {
			t.Fatalf("undeclared transitive dependency = %v, want a declaration error", err)
		}
	})

	t.Run("registry 404", func(t *testing.T) {
		missing := &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		})}
		var diagnostics bytes.Buffer
		prepared, err := Prepare(t.Context(), m, Options{Apps: names, HTTPClient: missing, Stderr: &diagnostics, Extract: fakeExtract(t, results, &calls)})
		if err != nil {
			t.Fatalf("unavailable registry record blocked generation: %v", err)
		}
		defer prepared.Close()
		if !strings.Contains(diagnostics.String(), "checkout: typed dependency orders unavailable:") {
			t.Fatalf("diagnostics = %s", &diagnostics)
		}
		if module := pythonModule(t, prepared, "checkout"); !strings.Contains(module, "async def call_tool(self, name: _builtins.str") {
			t.Fatalf("generated client does not expose untyped tool calls:\n%s", module)
		}
	})
}

func TestListedDirectoriesProvideEachOther(t *testing.T) {
	root := t.TempDir()
	checkout, orders := filepath.Join(root, "checkout"), filepath.Join(root, "orders")
	// checkout requires orders under the alias "store"; the provider takes that name.
	writeProvider(t, checkout, "checkout", "from holon.apps import apps\n"+pythonProvider, map[string]Reference{
		"store": {Name: "example.com/orders", Version: "1.0.0", Registry: "https://registry.unavailable.example"},
	})
	writeProvider(t, orders, "orders", "from holon.apps import apps\n"+pythonProvider, nil)
	for _, dir := range []string{checkout, orders} {
		put(t, dir, "pyproject.toml", "[project]\nname = 'app'\ndependencies = ['holon-sh']\n")
	}
	m, names, err := Directories([]string{orders, checkout})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"store", "checkout"}) {
		t.Fatalf("names = %v, want [store checkout]", names)
	}
	if got, want := strings.Join(appSummary(m), " "), "store:path="+orders+"+server checkout:path="+checkout+"+server"; got != want {
		t.Fatalf("apps = %s, want %s", got, want)
	}
	offline := &http.Client{Transport: unavailableRegistry{t}}
	results := map[string]extraction{"checkout": {UsesApps: true}, "store": {UsesApps: true, Tools: []mcpcontract.Tool{lookupTool}}}
	var calls []string
	before := map[string]map[string]string{checkout: fileMap(t, checkout), orders: fileMap(t, orders)}
	var output bytes.Buffer
	if err = Generate(t.Context(), m, Options{Apps: names, NoInstall: true, HTTPClient: offline, Stdout: &output, Stderr: &output, Extract: fakeExtract(t, results, &calls)}); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	if module := read(t, checkout, "holon/apps/__init__.py"); !strings.Contains(module, "async def lookup(") {
		t.Fatalf("checkout is not typed from the listed provider:\n%s", module)
	}
	for dir, files := range before {
		after := fileMap(t, dir)
		if !strings.HasPrefix(after["holon/apps/__init__.py"], "# "+mcpcontract.Marker) {
			t.Fatalf("%s did not get its own slot:\n%s", dir, after["holon/apps/__init__.py"])
		}
		delete(after, "holon/apps/__init__.py")
		if !maps.Equal(files, after) {
			t.Fatalf("generation changed files outside %s's slot", dir)
		}
	}

	t.Run("identity mismatch", func(t *testing.T) {
		put(t, orders, "server.json", string(mustJSON(t, map[string]any{"name": "example.com/orders", "version": "2.0.0", "description": "orders"})))
		m, names, err := Directories([]string{checkout, orders})
		if err != nil {
			t.Fatal(err)
		}
		_, err = Prepare(t.Context(), m, Options{Apps: names, HTTPClient: offline, Extract: fakeExtract(t, results, &calls)})
		if err == nil || !strings.Contains(err.Error(), "checkout requires store to be example.com/orders@1.0.0; declared app does not match") {
			t.Fatalf("mismatched provider = %v", err)
		}
	})
}

func TestDirectoriesRejectAmbiguousInputs(t *testing.T) {
	root := t.TempDir()
	dir := func(name, server string, requires map[string]Reference) string {
		path := filepath.Join(root, name)
		put(t, path, "server.json", string(mustJSON(t, map[string]any{
			"name": server, "version": "1.0.0", "description": name,
			"_meta": map[string]any{PublisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": requires}}},
		})))
		return path
	}
	orders := dir("orders", "example.com/orders", nil)
	ordersRef := Reference{Name: "example.com/orders", Version: "1.0.0"}
	checkout := dir("checkout", "example.com/checkout", map[string]Reference{"store": ordersRef})
	billing := dir("billing", "example.com/billing", map[string]Reference{"orders": ordersRef})
	otherOrders := dir("other-orders", "other.org/orders", nil)
	digit := dir("digit", "example.com/9lives", nil)
	put(t, root, "empty/.keep", "")
	put(t, root, "file", "")
	put(t, root, "malformed/server.json", `{"name":"example.com/x"`)
	for _, test := range []struct {
		name string
		dirs []string
		want []string
	}{
		{"no directories", nil, []string{"no directories"}},
		{"no server.json", []string{orders, filepath.Join(root, "empty")}, []string{filepath.Join(root, "empty") + ": no server.json found", "pass DIR..."}},
		{"file", []string{filepath.Join(root, "file")}, []string{"is not a directory"}},
		{"malformed server.json", []string{filepath.Join(root, "malformed")}, []string{filepath.Join(root, "malformed", "server.json") + ": "}},
		{"provider under two aliases", []string{checkout, billing, orders}, []string{orders + " is required as both", checkout, billing}},
		{"same derived name", []string{orders, otherOrders}, []string{orders, otherOrders, "app name orders"}},
		{"same server twice", []string{orders, orders}, []string{"both declare server example.com/orders"}},
		{"invalid derived name", []string{digit}, []string{digit, "example.com/9lives", "does not give a valid app name"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := Directories(test.dirs)
			if err == nil {
				t.Fatal("accepted")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestServerKey(t *testing.T) {
	long := strings.Repeat("a", 48) + "-" + strings.Repeat("b", 11)
	for input, want := range map[string]string{
		"example.com/checkout":          "checkout",
		"io.github.Some_User/My.Server": "my-server",
		"example.com/Some__Weird..Name": "some-weird-name",
		"example.com/---":               "",
		"example.com/" + long:           strings.Repeat("a", 48),
		"no-namespace":                  "no-namespace",
	} {
		if got := serverKey(input); got != want {
			t.Errorf("serverKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDirectoriesContractIsOffline(t *testing.T) {
	if _, err := typeLanguages["python"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
	dir := filepath.Join(t.TempDir(), "src")
	writeProvider(t, dir, "checkout", "from holon.apps import apps\n"+pythonProvider, map[string]Reference{
		"orders": {Name: "example.com/orders", Version: "1.0.0", Registry: "https://registry.unavailable.example"},
	})
	m, names, err := Directories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := Contract(t.Context(), m, names[0], Options{HTTPClient: &http.Client{Transport: unavailableRegistry{t}}})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Identity != (mcpcontract.Identity{Name: "example.com/checkout", Version: "1.0.0"}) {
		t.Fatalf("identity = %+v", metadata.Identity)
	}
	if metadata.Contract == nil || len(metadata.Contract.Tools) != 1 || metadata.Contract.Tools[0].Name != "read" {
		t.Fatalf("contract = %+v", metadata.Contract)
	}
}
