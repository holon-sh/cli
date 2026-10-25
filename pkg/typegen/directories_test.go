package typegen

import (
	"bytes"
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
}

func TestListedDirectories(t *testing.T) {
	for _, test := range []struct {
		name, file, source, alias, wantClient, wantError string
		ref                                              Reference
		providerImports                                  bool
		wantNames                                        []string
	}{
		{name: "inferred Python", file: "client.py", source: "from holon.apps import apps\nx = apps.orders, apps.checkout, apps.unknown\n", wantClient: "orders", wantNames: []string{"checkout"}},
		{name: "inferred TypeScript", file: "ui/client.ts", source: "import { createApps } from 'holon-ui'; apps['orders'].tools.lookup({});", wantClient: "orders", wantNames: []string{"checkout"}},
		{name: "declared alias", file: "client.py", source: "x = apps.orders, apps.store\n", alias: "store", ref: Reference{Name: "example.com/orders", Version: "1.0.0"}, providerImports: true, wantClient: "store", wantNames: []string{"store", "checkout"}},
		{name: "pinned version", file: "client.py", source: "x = apps.orders\n", alias: "orders", ref: Reference{Name: "example.com/orders", Version: "2.0.0"}, wantError: "checkout requires orders to be example.com/orders@2.0.0; declared app does not match"},
		{name: "declared name", file: "client.py", source: "x = apps.orders\n", alias: "orders", ref: Reference{Name: "other.org/orders", Version: "1.0.0"}, wantError: "checkout requires orders to be other.org/orders@1.0.0; declared app does not match"},
		{name: "unknown reference", file: "client.py", source: "x = apps['unknown-store']\n", wantNames: []string{"checkout"}},
		{name: "providers only", file: "client.py", source: "x = 1\n", wantNames: []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			checkout, orders := filepath.Join(root, "checkout"), filepath.Join(root, "orders")
			requires := map[string]Reference{}
			if test.alias != "" {
				requires[test.alias] = test.ref
			}
			writeProvider(t, checkout, "checkout", pythonProvider, requires)
			put(t, checkout, test.file, test.source)
			providerSource := pythonProvider
			if test.providerImports {
				providerSource = "from holon.apps import apps\n" + providerSource
			}
			writeProvider(t, orders, "orders", providerSource, nil)
			// Installed dependencies and generated files must not make a provider a caller.
			put(t, orders, "node_modules/other/index.js", "import 'holon-sh'; apps.checkout;")
			put(t, orders, ".venv/lib/client.py", "from holon.apps import apps\nx = apps.checkout\n")
			put(t, orders, "holon/apps/__init__.py", "# "+mcpcontract.Marker+" app=orders\n# apps.checkout\n")
			m, names, err := Directories([]string{orders, checkout})
			if err != nil {
				t.Fatal(err)
			}
			before := map[string]map[string]string{checkout: fileMap(t, checkout), orders: fileMap(t, orders)}
			provider := "orders"
			if test.alias != "" {
				provider = test.alias
			}
			results := map[string]extraction{"checkout": {UsesApps: true}, provider: {UsesApps: test.providerImports, Tools: []mcpcontract.Tool{lookupTool}}}
			var calls []string
			var output bytes.Buffer
			err = Generate(t.Context(), m, Options{Apps: names, NoInstall: true, HTTPClient: &http.Client{Transport: unavailableRegistry{t}}, Stdout: &output, Stderr: &output, Extract: fakeExtract(t, results, &calls)})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("generate = %v, want %s", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("generate: %v\n%s", err, &output)
			}
			if !slices.Equal(names, test.wantNames) {
				t.Fatalf("selected apps = %v, want %v", names, test.wantNames)
			}
			for name, dir := range map[string]string{"checkout": checkout, provider: orders} {
				after := fileMap(t, dir)
				if slices.Contains(test.wantNames, name) {
					module := after["holon/apps/__init__.py"]
					if !strings.HasPrefix(module, "# "+mcpcontract.Marker) {
						t.Fatalf("%s has no generated clients:\n%s", name, module)
					}
					if name == "checkout" && (strings.Contains(module, "async def lookup(") != (test.wantClient != "") || test.wantClient != "" && !strings.Contains(module, "\""+test.wantClient+"\"")) {
						t.Fatalf("checkout client should bind %q:\n%s", test.wantClient, module)
					}
					delete(after, "holon/apps/__init__.py")
					delete(before[dir], "holon/apps/__init__.py")
				} else if strings.Contains(output.String(), name+":") {
					t.Fatalf("output mentions provider-only %s:\n%s", name, &output)
				}
				if !maps.Equal(before[dir], after) {
					t.Fatalf("generation changed files outside %s's selected slot", name)
				}
			}
		})
	}
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
		"io.github.Some_User/Some__Weird..Name": "some-weird-name",
		"example.com/---":                       "",
		"example.com/" + long:                   strings.Repeat("a", 48),
		"no-namespace":                          "no-namespace",
	} {
		if got := serverKey(input); got != want {
			t.Errorf("serverKey(%q) = %q, want %q", input, got, want)
		}
	}
}
