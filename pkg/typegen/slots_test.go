package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func put(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func read(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func slotOwner(name string) slotMetadata {
	return slotMetadata{Marker: mcpcontract.Marker, App: name, Identity: mcpcontract.Identity{Name: "example.com/" + name, Version: "1"}, Digests: map[string]string{"orders": "sha256:one"}}
}

func TestNodeSlotResolvesSymlinkAndPreservesForeignContents(t *testing.T) {
	for _, pkg := range []string{"holon-sh", "holon-ui"} {
		t.Run(pkg, func(t *testing.T) { testNodeSlotResolvesSymlinkAndPreservesForeignContents(t, pkg) })
	}
}

func testNodeSlotResolvesSymlinkAndPreservesForeignContents(t *testing.T, pkg string) {
	root := t.TempDir()
	store := filepath.Join(root, "store")
	app := filepath.Join(root, "app")
	put(t, store, "package.json", `{"holon":{"slot":"generated","contractVersion":1}}`)
	if err := os.MkdirAll(filepath.Join(app, "node_modules/@holon-sh"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(store, filepath.Join(app, "node_modules", pkg)); err != nil {
		t.Fatal(err)
	}
	resolved, _, err := nodeSlot(app, pkg)
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(store)
	if resolved != real {
		t.Fatalf("slot = %s, want %s", resolved, real)
	}
	put(t, store, "generated/index.js", "user content")
	if err := writeNodeSlot(resolved, pkg, map[string]string{"index.js": "generated"}, slotOwner("one")); err == nil {
		t.Fatal("overwrote foreign slot")
	}
	if got := read(t, store, "generated/index.js"); got != "user content" {
		t.Fatalf("foreign contents changed: %s", got)
	}
	put(t, store, "generated/holon-generated.json", string(mustJSON(t, slotMetadata{Marker: mcpcontract.Marker, Placeholder: true})))
	if err := writeNodeSlot(resolved, pkg, map[string]string{"index.js": "generated"}, slotOwner("one")); err != nil {
		t.Fatal(err)
	}
	first, _ := os.Stat(filepath.Join(store, "generated/index.js"))
	if err := writeNodeSlot(resolved, pkg, map[string]string{"index.js": "generated"}, slotOwner("one")); err != nil {
		t.Fatal(err)
	}
	second, _ := os.Stat(filepath.Join(store, "generated/index.js"))
	if !os.SameFile(first, second) {
		t.Fatal("unchanged generation replaced the slot")
	}
	updated := slotOwner("one")
	updated.Identity.Version = "2"
	if err := writeNodeSlot(resolved, pkg, map[string]string{"index.js": "version two"}, updated); err != nil {
		t.Fatalf("same app cannot regenerate after a version bump: %v", err)
	}
	if err := writeNodeSlot(resolved, pkg, map[string]string{"index.js": "other"}, slotOwner("two")); err == nil || !strings.Contains(err.Error(), "install a separate "+pkg) {
		t.Fatalf("shared package was reassigned to another app: %v", err)
	}
	if got := read(t, store, "generated/index.js"); got != "version two" {
		t.Fatal("other app overwrote owner")
	}
}

func TestPythonSlotMigratesManagedRuntimeAndProtectsUserFiles(t *testing.T) {
	content := "# " + mcpcontract.Marker + " app=orders\napps = object()\n"
	files := map[string]string{"holon/apps/__init__.py": content}
	t.Run("legacy", func(t *testing.T) {
		root := t.TempDir()
		put(t, root, "holon/package.json", `{"holon":"holon-generated-apps-v1"}`)
		put(t, root, "holon/__init__.py", "legacy")
		put(t, root, "holon/client.py", "legacy runtime")
		if err := writePythonSlot(root, files); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"holon/__init__.py", "holon/client.py", "holon/package.json"} {
			if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
				t.Fatalf("legacy %s remains: %v", name, err)
			}
		}
		if got := read(t, root, "holon/apps/__init__.py"); got != content {
			t.Fatal("wrong generated module")
		}
	})
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
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, "holon")); err != nil {
			t.Fatal(err)
		}
		if err := writePythonSlot(root, files); err == nil {
			t.Fatal("followed foreign namespace symlink")
		}
		entries, _ := os.ReadDir(outside)
		if len(entries) != 0 {
			t.Fatal("wrote outside app")
		}
	})
}

func TestGenerateRejectsSharedNodeSlotBeforeWritingEitherApp(t *testing.T) {
	if _, err := typeLanguages["typescript"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	pkg := filepath.Join(root, "node_modules/holon-sh")
	put(t, pkg, "package.json", `{"holon":{"slot":"generated","contractVersion":1}}`)
	put(t, pkg, "generated/holon-generated.json", string(mustJSON(t, slotMetadata{Marker: mcpcontract.Marker, Placeholder: true})))
	put(t, pkg, "generated/index.js", "placeholder")
	m := Manifest{}
	for _, name := range []string{"first", "second"} {
		app := filepath.Join(root, name)
		put(t, app, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
		m.Apps = append(m.Apps, App{Name: name, Path: app})
	}
	err := Generate(context.Background(), m, Options{NoInstall: true})
	if err == nil || !strings.Contains(err.Error(), "share") {
		t.Fatalf("shared slot not diagnosed: %v", err)
	}
	if got := read(t, pkg, "generated/index.js"); got != "placeholder" {
		t.Fatal("shared slot changed before rejection")
	}
}

func TestCheckDoesNotChangeSourceOrInstalledSlot(t *testing.T) {
	if _, err := typeLanguages["typescript"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
	app := t.TempDir()
	pkg := filepath.Join(app, "node_modules/holon-sh")
	ui := filepath.Join(app, "node_modules/holon-ui")
	put(t, app, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1","holon-ui":"^0.0.1"}}`)
	put(t, app, "src/index.ts", `export const answer: string = "ok";`)
	put(t, app, "src/view.ts", `import type { Inputs } from "holon-ui"; export const inputs: Inputs = {};`)
	put(t, pkg, "package.json", `{"holon":{"slot":"generated","contractVersion":1}}`)
	put(t, pkg, "generated/index.js", "foreign marker must survive check")
	put(t, ui, "package.json", `{"exports":{".":{"types":"./generated/index.d.ts"}},"holon":{"slot":"generated","contractVersion":1}}`)
	put(t, ui, "generated/index.d.ts", "export interface Inputs { stale: never }")
	contents := func() map[string]string {
		files := map[string]string{}
		err := filepath.WalkDir(app, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				files[path] = string(data)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := contents()
	var output bytes.Buffer
	err := Check(context.Background(), Manifest{Apps: []App{{Name: "app", Path: app}}}, Options{NoPublished: true, Stdout: &output, Stderr: &output})
	if err != nil {
		t.Fatalf("check: %v\n%s", err, &output)
	}
	if !maps.Equal(before, contents()) {
		t.Fatal("check modified application files or the installed SDK")
	}
}

func TestDevDependencyNodeSlotGenerationAndCheck(t *testing.T) {
	if _, err := typeLanguages["typescript"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
	app := t.TempDir()
	pkg := filepath.Join(app, "node_modules/holon-sh")
	put(t, app, "package.json", `{"type":"module","devDependencies":{"holon-sh":"^0.0.1"}}`)
	put(t, app, "src/index.ts", `import type { Inputs } from "holon-sh"; const inputs: Inputs = {};`)
	put(t, pkg, "package.json", `{"exports":{".":{"types":"./generated/index.d.ts"}},"holon":{"slot":"generated","contractVersion":1}}`)
	var output bytes.Buffer
	manifest := Manifest{Apps: []App{{Name: "app", Path: app}}}
	options := Options{NoInstall: true, NoPublished: true, Stdout: &output, Stderr: &output}
	if err := Generate(t.Context(), manifest, options); err != nil {
		t.Fatalf("generate dev dependency slot: %v\n%s", err, &output)
	}
	if strings.Contains(output.String(), "npm install") {
		t.Fatalf("installed a second runtime: %s", &output)
	}
	if !isFile(filepath.Join(pkg, "generated/index.d.ts")) {
		t.Fatal("generation did not write the slot")
	}
	// Check must use fresh declarations in its copy and preserve the stale
	// installed declarations. Linking the original package would fail the check.
	const stale = "export interface Inputs { stale: never }"
	put(t, pkg, "generated/index.d.ts", stale)
	if err := Check(t.Context(), manifest, options); err != nil {
		t.Fatalf("check dev dependency slot: %v\n%s", err, &output)
	}
	if got := read(t, pkg, "generated/index.d.ts"); got != stale {
		t.Fatal("checking changed the installed slot")
	}
}

// slotPackage installs a minimal placeholder runtime package that declares a generation slot.
func slotPackage(t *testing.T, app, pkg string) string {
	t.Helper()
	dir := filepath.Join(app, "node_modules", pkg)
	put(t, dir, "package.json", `{"name":"`+pkg+`","holon":{"slot":"generated","contractVersion":1}}`)
	put(t, dir, "generated/holon-generated.json", string(mustJSON(t, slotMetadata{Marker: mcpcontract.Marker, Placeholder: true})))
	return dir
}

// typeScriptCaller writes a TypeScript app requiring a Python orders provider, with the given package.json.
func typeScriptCaller(t *testing.T, packageJSON string) (Manifest, func(context.Context, string, string, string, string) error) {
	t.Helper()
	root := t.TempDir()
	app, orders := filepath.Join(root, "app"), filepath.Join(root, "orders")
	writeProvider(t, orders, "orders", pythonProvider, nil)
	put(t, app, "package.json", packageJSON)
	put(t, app, "server.ts", "export default {};\n")
	put(t, app, "server.json", string(mustJSON(t, map[string]any{
		"name": "example.com/app", "version": "1.0.0", "description": "app",
		"_meta": map[string]any{PublisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": map[string]Reference{"orders": {Name: "example.com/orders", Version: "1.0.0"}}}}},
	})))
	manifest := Manifest{Apps: []App{{Name: "app", Path: app}, {Name: "orders", Path: orders, Access: json.RawMessage(`{"server":{}}`)}}}
	own := mcpcontract.Tool{Name: "status", Input: json.RawMessage(`{"type":"object","additionalProperties":false}`)}
	results := map[string]extraction{"app": {UsesApps: true, Tools: []mcpcontract.Tool{own}}, "orders": {Tools: []mcpcontract.Tool{lookupTool}}}
	var calls []string
	return manifest, fakeExtract(t, results, &calls)
}

func TestGenerateWritesDeclaredSlots(t *testing.T) {
	manifest, extract := typeScriptCaller(t, `{"type":"module","dependencies":{"holon-sh":"^0.0.1","holon-ui":"^0.0.1"}}`)
	app := manifest.Apps[0].Path
	node, ui := slotPackage(t, app, "holon-sh"), slotPackage(t, app, "holon-ui")
	var output bytes.Buffer
	if err := Generate(t.Context(), manifest, Options{Apps: []string{"app"}, NoInstall: true, Stdout: &output, Extract: extract}); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	if got, want := output.String(), "app: generated browser types\napp: generated typescript types\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	browser := read(t, ui, "generated/index.js")
	for _, want := range []string{`import { bindApps } from "../runtime.mjs";`, "exposeTools", `"self":{"tools":[{"name":"status"`, `"lookup"`} {
		if !strings.Contains(browser, want) {
			t.Fatalf("holon-ui index.js lacks %s:\n%s", want, browser)
		}
	}
	declarations := read(t, ui, "generated/index.d.ts")
	for _, want := range []string{`from "holon-core/protocol"`, `from "./resources.js"`, "exposeTools"} {
		if !strings.Contains(declarations, want) {
			t.Fatalf("holon-ui index.d.ts lacks %s:\n%s", want, declarations)
		}
	}
	// The app's own server is exposed under the reserved key self, not its app name.
	if !strings.Contains(declarations, `"self": {`) || strings.Contains(browser, `"app":{`) {
		t.Fatalf("own contract is not keyed self:\n%s\n%s", browser, declarations)
	}
	if !isFile(filepath.Join(ui, "generated/resources.d.ts")) {
		t.Fatal("holon-ui slot has no resources.d.ts")
	}
	nodeIndex := read(t, node, "generated/index.js")
	for _, want := range []string{`import { createApps as create, bindApps } from "../runtime.mjs";`, "export const apps = create(", `"lookup"`} {
		if !strings.Contains(nodeIndex, want) {
			t.Fatalf("holon-sh index.js lacks %s:\n%s", want, nodeIndex)
		}
	}
	if strings.Contains(nodeIndex, `"status"`) || isFile(filepath.Join(node, "generated/browser.js")) {
		t.Fatal("holon-sh slot received browser clients")
	}
	for _, pkg := range []string{node, ui} {
		var marker slotMetadata
		if err := readTypeJSON(filepath.Join(pkg, "generated/holon-generated.json"), &marker); err != nil || marker.App != "app" || marker.Placeholder {
			t.Fatalf("%s marker = %+v, %v", pkg, marker, err)
		}
	}
}

func TestGenerateUIOnlyApp(t *testing.T) {
	manifest, extract := typeScriptCaller(t, `{"type":"module","dependencies":{"holon-ui":"^0.0.1"}}`)
	app := manifest.Apps[0].Path
	put(t, app, "view.ts", `import { createApps } from "holon-ui";`)
	ui := slotPackage(t, app, "holon-ui")
	var output bytes.Buffer
	if err := Generate(t.Context(), manifest, Options{Apps: []string{"app"}, NoInstall: true, Stdout: &output, Extract: extract}); err != nil {
		t.Fatalf("generate without holon-sh: %v\n%s", err, &output)
	}
	if got, want := output.String(), "app: generated browser types\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if !isFile(filepath.Join(ui, "generated/index.js")) {
		t.Fatal("holon-ui slot was not written")
	}
}
