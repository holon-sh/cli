package typegen

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/holon-sh/cli/pkg/tooling/core"
)

// Exercise the shipped compatibility entrypoints without SDKs or site-packages.
// The same table protects both languages' manifest and post-build contracts.
func TestBundledRunnersPreserveSourceIntegrity(t *testing.T) {
	assets := t.TempDir()
	if err := tooling.Unpack(assets); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ language, entry, source string }{
		{"python", "server.py", strings.ReplaceAll(pythonProvider+"raise RuntimeError('must not execute')\n", "\n", "\r\n")},
		{"typescript", "server.ts", `import {McpServer,createMcpHandler,fromJsonSchema} from "@modelcontextprotocol/server"; import schema from "./schema.json"; export default createMcpHandler(()=>{const s=new McpServer({name:"test",version:"1"}); s.registerTool("read",{inputSchema:fromJsonSchema(schema)},()=>({content:[]})); return s;});`},
	} {
		t.Run(test.language, func(t *testing.T) {
			language := typeLanguages[test.language]
			program, err := language.localProgram(t.Context())
			if err != nil {
				t.Skip(err)
			}
			app := t.TempDir()
			put(t, app, test.entry, test.source)
			put(t, app, "schema.json", `{"type":"object","properties":{"id":{"type":"string","pattern":"^(?=.*[A-Z]).+$"}}}`)
			put(t, app, "unused.py", "# unimported sources are guarded too\r\n")
			put(t, app, "holon/apps/__init__.py", "not valid Python")
			config, output := filepath.Join(app, "config.json"), filepath.Join(app, "result.json")
			run := func(mode string, value any) (string, error) {
				t.Helper()
				if err := writeJSON(config, value); err != nil {
					t.Fatal(err)
				}
				args := []string{filepath.Join(assets, test.language, language.runner), mode, config, output}
				if test.language == "python" {
					args = append([]string{"-S"}, args...)
				}
				command := exec.CommandContext(t.Context(), program, args...)
				command.Dir = app
				data, err := command.CombinedOutput()
				return string(data), err
			}
			extract := func() extraction {
				t.Helper()
				if text, err := run("extract", map[string]any{"apps": []extractionEntry{{Name: "app", Path: app, Entry: test.entry}}}); err != nil {
					t.Fatalf("extract: %v\n%s", err, text)
				}
				var results map[string]extraction
				if err := readTypeJSON(output, &results); err != nil {
					t.Fatal(err)
				}
				return results["app"]
			}
			result := extract()
			if err := result.Validate(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(result.Tools) != 1 || result.Tools[0].Name != "read" || len(result.Sources) != 2 {
				t.Fatalf("extraction = %+v", result)
			}
			check := core.CheckConfig{Root: app, Sources: result.Sources, Entry: test.entry, Check: true}
			if text, err := run("verify", check); err != nil {
				t.Fatalf("fresh manifest: %v\n%s", err, text)
			}
			duplicate := test.source + "\n@server.tool(name='read')\ndef again(id: str) -> str: return id\n"
			if test.language == "typescript" {
				duplicate = strings.Replace(test.source, "return s;", `s.registerTool("read",{inputSchema:fromJsonSchema(schema)},()=>({content:[]})); return s;`, 1)
			}
			put(t, app, test.entry, duplicate)
			if result := extract(); !strings.Contains(result.Diagnostic, "nonempty and unique") {
				t.Fatalf("duplicate catalog was not rejected: %+v", result)
			}
			put(t, app, test.entry, test.source)
			if test.language == "typescript" {
				put(t, app, "schema.json", `{"type":"object","properties":{"id":{"type":"unsupported"}}}`)
				if result := extract(); !strings.Contains(result.Diagnostic, "unsupported JSON Schema") {
					t.Fatalf("invalid schema was not rejected: %+v", result)
				}
			}
			changed := "schema.json"
			if test.language == "python" {
				changed = "unused.py"
			}
			put(t, app, changed, "changed")
			for _, mode := range []string{"check", "verify"} {
				if text, err := run(mode, check); err == nil || !strings.Contains(text, "refusing to publish stale types") {
					t.Fatalf("%s accepted changed sources: %v\n%s", mode, err, text)
				}
			}
			if err := os.Remove(filepath.Join(app, changed)); err != nil {
				t.Fatal(err)
			}
			if text, err := run("verify", check); err == nil || !strings.Contains(text, changed) {
				t.Fatalf("verify accepted missing source: %v\n%s", err, text)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			runner := core.Runner{Language: test.language, Program: program, Path: filepath.Join(assets, test.language, language.runner)}
			if _, err := runner.Extract(ctx, []core.App{{Path: app, Entry: test.entry}}); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation = %v", err)
			}
		})
	}
}

func TestResolveValidatesExternalExtractionContracts(t *testing.T) {
	app := t.TempDir()
	writeProvider(t, app, "invalid", pythonProvider)
	// A null output schema must not turn into an absent schema while crossing
	// the hook's JSON boundary. Other contract rules are covered by mcptype.
	_, err := Resolve(t.Context(), Source{Path: app}, Options{Extract: func(_ context.Context, _, _, _, output string) error {
		return os.WriteFile(output, []byte(`{"source":{"tools":[{"name":"x","inputSchema":{"type":"object"},"outputSchema":null}]}}`), 0o600)
	}})
	if err == nil || !strings.Contains(err.Error(), "expected a map") {
		t.Fatalf("Resolve accepted a null schema or failed for another reason: %v", err)
	}
}
