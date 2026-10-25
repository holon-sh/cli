package typegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"oras.land/oras-go/v2/registry/remote/auth"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

var lookupTool = mcpcontract.Tool{Name: "lookup", Input: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
	Output: json.RawMessage(`{"type":"object","properties":{"total":{"type":"number"}},"required":["total"],"additionalProperties":false}`)}

func ordersMetadata() *mcpcontract.Metadata {
	return &mcpcontract.Metadata{Version: mcpcontract.Version, Identity: mcpcontract.Identity{Name: "example.com/orders", Version: "1.0.0"},
		Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{lookupTool}}}
}

// fakeExtract stands in for the language runners: it answers each configured app from results
// and records the languages it was asked for.
func fakeExtract(t *testing.T, results map[string]extraction, calls *[]string) func(context.Context, string, string, string, string) error {
	t.Helper()
	return func(_ context.Context, language, tooling, config, output string) error {
		*calls = append(*calls, language)
		if !isFile(filepath.Join(tooling, language, typeLanguages[language].runner)) {
			return fmt.Errorf("tooling for %s is not unpacked under %s", language, tooling)
		}
		var request struct {
			Apps []struct{ Name, Path, Entry string }
		}
		if err := readTypeJSON(config, &request); err != nil {
			return err
		}
		found := map[string]extraction{}
		for _, app := range request.Apps {
			if !isFile(filepath.Join(app.Path, app.Entry)) {
				return fmt.Errorf("%s entry %s is not in its snapshot", app.Name, app.Entry)
			}
			found[app.Name] = results[app.Name]
		}
		return writeJSON(output, found)
	}
}

func TestExtractHookReplacesHostInterpreter(t *testing.T) {
	var calls []string
	results := map[string]extraction{
		"caller": {UsesApps: true, Sources: map[string]string{"server.py": "sha256:caller"}},
		"orders": {Tools: []mcpcontract.Tool{lookupTool}},
	}
	root := t.TempDir()
	writeProvider(t, filepath.Join(root, "caller"), "caller", "from holon.apps import apps\n"+pythonProvider, map[string]Reference{
		"orders": {Name: "example.com/orders", Version: "1.0.0"},
	})
	writeProvider(t, filepath.Join(root, "orders"), "orders", pythonProvider, nil)
	manifest := Manifest{Apps: []App{{Name: "caller", Path: filepath.Join(root, "caller")}, {Name: "orders", Path: filepath.Join(root, "orders"), Access: json.RawMessage(`{"server":{}}`)}}}
	prepared, err := Prepare(t.Context(), manifest, Options{Extract: fakeExtract(t, results, &calls)})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if !slices.Equal(calls, []string{"python"}) {
		t.Fatalf("extractor calls = %v, want one Python batch", calls)
	}
	if expected := prepared.Metadata()["caller"].Requires["orders"]; len(expected.Contract.Tools) != 1 {
		t.Fatalf("hook result was not used for the caller's expectations: %+v", expected)
	}
	stage := t.TempDir()
	if err = prepared.WritePythonSlot("caller", stage); err != nil {
		t.Fatal(err)
	}
	if module := read(t, stage, "holon/apps/__init__.py"); !strings.Contains(module, "async def lookup(") {
		t.Fatalf("caller client is not typed from the hook's extraction:\n%s", module)
	}

	failure := errors.New("buildx unavailable")
	_, err = Prepare(t.Context(), manifest, Options{Extract: func(context.Context, string, string, string, string) error { return failure }})
	if !errors.Is(err, failure) {
		t.Fatalf("extractor failure = %v, want %v", err, failure)
	}
}

func TestPreparedSlotWritersAndGenerateExtractOnce(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	writeProvider(t, app, "app", "from holon.apps import apps\n"+pythonProvider, map[string]Reference{"other": {Name: "example.com/other", Version: "1.0.0"}})
	put(t, app, "pyproject.toml", "[project]\nname = 'app'\ndependencies = ['holon-sh']\n")
	put(t, app, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1","holon-ui":"^0.0.1"}}`)
	pkg := filepath.Join(app, "node_modules/holon-sh")
	put(t, pkg, "package.json", `{"holon":{"slot":"generated","contractVersion":1}}`)
	uiPkg := filepath.Join(app, "node_modules/holon-ui")
	put(t, uiPkg, "package.json", `{"holon":{"slot":"generated","contractVersion":1}}`)
	other := filepath.Join(root, "other")
	writeProvider(t, other, "other", pythonProvider, nil)
	manifest := Manifest{Apps: []App{{Name: "app", Path: app}, {Name: "other", Path: other, Access: json.RawMessage(`{"server":{}}`)}}}
	results := map[string]extraction{"app": {UsesApps: true, Tools: []mcpcontract.Tool{lookupTool}}}

	var calls []string
	prepared, err := Prepare(t.Context(), manifest, Options{Apps: []string{"app"}, Extract: fakeExtract(t, results, &calls)})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	stage := t.TempDir()
	put(t, stage, "holon/package.json", `{"holon":"`+mcpcontract.Marker+`"}`)
	put(t, stage, "holon/client.py", "legacy runtime")
	if err = prepared.WritePythonSlot("app", stage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(stage, "holon/client.py")); !os.IsNotExist(err) {
		t.Fatalf("legacy managed runtime was not migrated: %v", err)
	}
	stagedPkg := filepath.Join(stage, "node_modules/holon-sh")
	if err = os.MkdirAll(stagedPkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = prepared.WriteNodeSlot("app", stagedPkg); err != nil {
		t.Fatal(err)
	}
	var marker slotMetadata
	if err = readTypeJSON(filepath.Join(stagedPkg, "generated/holon-generated.json"), &marker); err != nil || marker.App != "app" || marker.Digests["self"] == "" {
		t.Fatalf("node slot marker = %+v, %v", marker, err)
	}
	if err = prepared.WriteNodeSlot("app", stagedPkg); err != nil {
		t.Fatalf("rewriting an identical slot: %v", err)
	}
	stagedUI := filepath.Join(stage, "node_modules/holon-ui")
	if err = os.MkdirAll(stagedUI, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = prepared.WriteBrowserSlot("app", stagedUI); err != nil {
		t.Fatal(err)
	}
	marker = slotMetadata{}
	if err = readTypeJSON(filepath.Join(stagedUI, "generated/holon-generated.json"), &marker); err != nil || marker.App != "app" || marker.Digests["self"] == "" {
		t.Fatalf("browser slot marker = %+v, %v", marker, err)
	}
	for _, test := range []struct{ name, want string }{{"other", "no prepared sources"}, {"missing", "unknown app"}} {
		if err := prepared.WriteNodeSlot(test.name, t.TempDir()); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("WriteNodeSlot(%s) = %v, want %q", test.name, err, test.want)
		}
	}
	if err := prepared.WriteGoSlot("app", t.TempDir()); err == nil || !strings.Contains(err.Error(), "no go clients") {
		t.Fatalf("WriteGoSlot without GoModule = %v", err)
	}

	// Generate writes the same slots from a single extraction pass.
	calls = nil
	var output bytes.Buffer
	if err = Generate(t.Context(), manifest, Options{Apps: []string{"app"}, NoInstall: true, Stdout: &output, Extract: fakeExtract(t, results, &calls)}); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	if !slices.Equal(calls, []string{"python"}) {
		t.Fatalf("generate extracted %v, want one Python batch", calls)
	}
	if got, want := output.String(), "app: generated browser types\napp: generated python types\napp: generated typescript types\n"; got != want {
		t.Fatalf("generate output = %q, want %q", got, want)
	}
	if read(t, app, "holon/apps/__init__.py") != read(t, stage, "holon/apps/__init__.py") {
		t.Fatal("Generate and WritePythonSlot wrote different modules")
	}
	for _, name := range []string{"holon-generated.json", "index.d.ts", "index.js"} {
		if read(t, pkg, "generated/"+name) != read(t, stagedPkg, "generated/"+name) {
			t.Fatalf("Generate and WriteNodeSlot wrote different %s", name)
		}
		if read(t, uiPkg, "generated/"+name) != read(t, stagedUI, "generated/"+name) {
			t.Fatalf("Generate and WriteBrowserSlot wrote different %s", name)
		}
	}

	// An app that does not declare holon-ui has no browser clients.
	put(t, app, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	nodeOnly, err := Prepare(t.Context(), manifest, Options{Apps: []string{"app"}, Extract: fakeExtract(t, results, &calls)})
	if err != nil {
		t.Fatal(err)
	}
	defer nodeOnly.Close()
	if err := nodeOnly.WriteBrowserSlot("app", t.TempDir()); err == nil || !strings.Contains(err.Error(), "no browser clients") {
		t.Fatalf("WriteBrowserSlot without holon-ui = %v", err)
	}
}

func TestCheckConfigDescribesTheRunnerCheck(t *testing.T) {
	root := t.TempDir()
	writeProvider(t, filepath.Join(root, "app"), "app", pythonProvider, nil)
	writeProvider(t, filepath.Join(root, "plain"), "plain", pythonProvider, nil)
	results := map[string]extraction{
		"app":   {Tools: []mcpcontract.Tool{lookupTool}, Sources: map[string]string{"server.py": "sha256:one"}},
		"plain": {Diagnostic: "dynamic tools"},
	}
	var calls []string
	prepared, err := Prepare(t.Context(), Manifest{Apps: []App{{Name: "app", Path: filepath.Join(root, "app")}, {Name: "plain", Path: filepath.Join(root, "plain")}}},
		Options{Extract: fakeExtract(t, results, &calls)})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	config, ok := prepared.CheckConfig("app")
	if want := `{"check":true,"entry":"server.py","sources":{"server.py":"sha256:one"}}`; !ok || string(mustJSON(t, config)) != want {
		t.Fatalf("CheckConfig(app) = %s %t, want %s", mustJSON(t, config), ok, want)
	}
	config, ok = prepared.CheckConfig("plain")
	if want := `{"check":false,"entry":"server.py","sources":{}}`; !ok || string(mustJSON(t, config)) != want {
		t.Fatalf("CheckConfig(plain) = %s %t, want %s", mustJSON(t, config), ok, want)
	}
	if _, ok := prepared.CheckConfig("missing"); ok {
		t.Fatal("unknown app has a check configuration")
	}
}

func TestTypeCheckReusesPreparation(t *testing.T) {
	if _, err := typeLanguages["typescript"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
	app := t.TempDir()
	put(t, app, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	put(t, app, "src/index.ts", `export const answer: number = "not a number";`)
	put(t, app, "node_modules/holon-sh/package.json", `{"holon":{"slot":"generated","contractVersion":1}}`)
	var output bytes.Buffer
	prepared, err := Prepare(t.Context(), Manifest{Apps: []App{{Name: "app", Path: app}}}, Options{Stdout: &output, Stderr: &output})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if err = prepared.TypeCheck(t.Context()); err == nil || !strings.Contains(err.Error(), "does not type-check") {
		t.Fatalf("TypeCheck = %v\n%s", err, &output)
	}
	put(t, app, "src/index.ts", `export const answer: number = 42;`)
	if err = prepared.TypeCheck(t.Context()); err == nil {
		t.Fatal("TypeCheck read the app instead of its prepared snapshot")
	}
}

// authenticatedRegistry serves imageClient's documents only with HTTP basic credentials user:secret.
func authenticatedRegistry(t *testing.T, metadata *mcpcontract.Metadata) *http.Client {
	t.Helper()
	inner := imageClient(t, metadata, mcpcontract.MediaType).Transport
	return &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
		if user, password, ok := request.BasicAuth(); !ok || user != "user" || password != "secret" {
			header := http.Header{"Www-Authenticate": {`Basic realm="images.example"`}, "Content-Type": {"application/json"}}
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: header, Request: request,
				Body: io.NopCloser(strings.NewReader(`{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`))}, nil
		}
		return inner.RoundTrip(request)
	})}
}

func TestCredentialAuthenticatesImageDependencies(t *testing.T) {
	results := map[string]extraction{"caller": {UsesApps: true}}
	grant := func(host, password string) auth.CredentialFunc {
		return auth.StaticCredential(host, auth.Credential{Username: "user", Password: password})
	}
	for _, test := range []struct {
		name       string
		credential auth.CredentialFunc
		docker     string // password stored for images.example in the Docker config
		want       string
		typed      bool
	}{
		{name: "grant", credential: grant("images.example", "secret"), typed: true},
		{name: "grant ahead of docker", credential: grant("images.example", "secret"), docker: "wrong", typed: true},
		{name: "docker for other registries", credential: grant("elsewhere.example", "wrong"), docker: "secret", typed: true},
		{name: "rejected grant", credential: grant("images.example", "wrong"), docker: "secret", want: "rejected the supplied registry credential"},
		{name: "rejected docker credential stays untyped", docker: "wrong"},
	} {
		t.Run(test.name, func(t *testing.T) {
			docker := t.TempDir()
			if test.docker != "" {
				encoded := base64.StdEncoding.EncodeToString([]byte("user:" + test.docker))
				put(t, docker, "config.json", `{"auths":{"images.example":{"auth":"`+encoded+`"}}}`)
			}
			var calls []string
			var diagnostics bytes.Buffer
			options := Options{Apps: []string{"caller"}, HTTPClient: authenticatedRegistry(t, ordersMetadata()), Env: []string{"DOCKER_CONFIG=" + docker},
				Stderr: &diagnostics, Credential: test.credential, Extract: fakeExtract(t, results, &calls)}
			prepared, err := Prepare(t.Context(), imageCaller(t, App{Image: "images.example/orders:1.0.0"}), options)
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("rejected credential = %v, want %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			metadata := prepared.Metadata()
			if _, typed := metadata["caller"].Requires["orders"]; typed != test.typed {
				t.Fatalf("typed = %t, want %t; diagnostics: %s", typed, test.typed, &diagnostics)
			}
			if !test.typed && !strings.Contains(diagnostics.String(), "typed dependency orders unavailable") {
				t.Fatalf("401 was not reported as untyped: %s", &diagnostics)
			}
			if orders := metadata["orders"]; test.typed && (orders == nil || orders.Identity != ordersMetadata().Identity) {
				t.Fatalf("Metadata omits the resolved image dependency: %+v", orders)
			}
		})
	}
}

func TestPublishedUsesCredential(t *testing.T) {
	root := t.TempDir()
	put(t, root, "server.py", pythonProvider)
	put(t, root, "server.json", string(mustJSON(t, map[string]any{"name": "example.com/orders", "version": "1.0.0", "description": "Orders",
		"packages": []any{map[string]string{"registryType": "oci", "identifier": "images.example/orders:1.0.0"}}})))
	var calls []string
	extract := fakeExtract(t, map[string]extraction{"orders": {Tools: []mcpcontract.Tool{lookupTool}}}, &calls)
	for _, test := range []struct {
		password string
		want     string
	}{{"secret", ""}, {"wrong", "rejected the supplied registry credential"}} {
		prepared, err := Prepare(t.Context(), Manifest{Apps: []App{{Name: "orders", Path: root}}}, Options{
			HTTPClient: authenticatedRegistry(t, ordersMetadata()), Env: []string{"DOCKER_CONFIG=" + t.TempDir()}, Extract: extract,
			Credential: auth.StaticCredential("images.example", auth.Credential{Username: "user", Password: test.password}),
		})
		if err != nil {
			t.Fatal(err)
		}
		published, err := prepared.Published(t.Context())
		if test.want != "" {
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Published with a rejected credential = %v", err)
			}
		} else if err != nil || !hasContract(published["orders"]) {
			t.Fatalf("Published = %+v, %v", published, err)
		} else if err = prepared.CheckPublished(t.Context()); err != nil {
			t.Fatalf("CheckPublished: %v", err)
		}
		_ = prepared.Close()
	}
}

func TestGoCallerClients(t *testing.T) {
	root := t.TempDir()
	put(t, root, "holon.yaml", string(mustJSON(t, Manifest{Apps: []App{
		{Name: "caller", Path: "caller"},
		{Name: "orders", Image: "images.example/orders:1.0.0", Access: json.RawMessage(`{"server":{}}`)},
	}})))
	caller := filepath.Join(root, "caller")
	put(t, caller, "go.mod", "module example.com/caller\n\ngo 1.27.0\n")
	put(t, caller, "server.go", "package main\n\nfunc main() {}\n")
	put(t, caller, "server.json", string(mustJSON(t, map[string]any{"name": "example.com/caller", "version": "1.0.0", "description": "Caller",
		"_meta": map[string]any{PublisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": map[string]Reference{
			"orders": {Name: "example.com/orders", Version: "1.0.0"},
		}}}}})))
	manifest, err := LoadManifest(filepath.Join(root, "holon.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Apps: []string{"caller"}, HTTPClient: imageClient(t, ordersMetadata(), mcpcontract.MediaType), Env: []string{"DOCKER_CONFIG=" + t.TempDir()},
		GoModule: "github.com/holon-sh/sdk/go", Extract: func(context.Context, string, string, string, string) error {
			return errors.New("Go callers are not extracted")
		}}
	prepared, err := Prepare(t.Context(), manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	callerMetadata := prepared.Metadata()["caller"]
	if callerMetadata.Contract != nil || len(callerMetadata.Requires["orders"].Contract.Tools) != 1 {
		t.Fatalf("Go caller metadata = %+v", callerMetadata)
	}
	if _, ok := prepared.CheckConfig("caller"); ok {
		t.Fatal("Go callers have no runner check")
	}
	if err = prepared.TypeCheck(t.Context()); err != nil {
		t.Fatalf("TypeCheck of a Go caller: %v", err)
	}
	stage := t.TempDir()
	if err = prepared.WriteGoSlot("caller", stage); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "golden", "go-caller")
	entries, err := os.ReadDir(filepath.Join(stage, "holon"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
		got := read(t, stage, filepath.Join("holon", entry.Name()))
		if *updateGolden {
			put(t, golden, entry.Name(), got)
		}
		if want := read(t, golden, entry.Name()); got != want {
			t.Errorf("%s differs from %s:\n%s", entry.Name(), golden, got)
		}
	}
	if !slices.Equal(names, []string{"clients.go", "package.json", "types.go"}) {
		t.Fatalf("Go package files = %v", names)
	}

	// Generate installs the same package into the app and refuses an unmarked holon/ directory.
	var output bytes.Buffer
	options.Stdout = &output
	if err = Generate(t.Context(), manifest, options); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if output.String() != "caller: generated go types\n" {
		t.Fatalf("generate output = %q", &output)
	}
	installed, _ := os.ReadDir(filepath.Join(caller, "holon"))
	if len(installed) != 3 || read(t, caller, "holon/clients.go") != read(t, stage, "holon/clients.go") {
		t.Fatal("Generate and WriteGoSlot wrote different Go packages")
	}
	other := t.TempDir()
	put(t, other, "holon/mine.go", "package holon\n")
	if err = prepared.WriteGoSlot("caller", other); err == nil || !strings.Contains(err.Error(), "unmarked") {
		t.Fatalf("overwrote a user holon/ package: %v", err)
	}
	if !maps.Equal(map[string]bool{"mine.go": true}, dirNames(t, filepath.Join(other, "holon"))) {
		t.Fatal("user package changed")
	}
}

func dirNames(t *testing.T, directory string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	return names
}
