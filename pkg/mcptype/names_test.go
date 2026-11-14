package mcptype

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// notebookTools replies {status, body} from every tool, with a Problem body from 400 up. Three tools
// take the same untitled {params, headers} input, two of them with a SaveRequest body as well, and
// create alone takes a CreateRequest.
func notebookTools() []mcpcontract.Tool {
	problem := `"Problem":{"type":"object","required":["title"],"properties":{"title":{"type":"string"},"detail":{"type":"string"}}}`
	params := `"NotebookParams":{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`
	headers := `"headers":{"type":"object","properties":{"if-match":{"type":"string"}}}`
	reply := func(body string, defs ...string) json.RawMessage {
		return json.RawMessage(`{"anyOf":[{"type":"object","required":["status","body"],"properties":{"status":{"const":200},"body":` + body + `}},` +
			`{"type":"object","required":["status","body"],"properties":{"status":{"type":"integer","minimum":400},"body":{"$ref":"#/$defs/Problem"}}}],` +
			`"$defs":{` + strings.Join(append(defs, problem), ",") + `}}`)
	}
	mutation := func(body ...string) json.RawMessage {
		properties := []string{`"params":{"$ref":"#/$defs/NotebookParams"}`, headers}
		defs := []string{params}
		if len(body) > 0 {
			properties = append(properties, `"body":{"$ref":"#/$defs/SaveRequest"}`)
			defs = append(defs, `"SaveRequest":{"type":"object","required":["content"],"properties":{"content":{"type":"string"}}}`)
		}
		return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + strings.Join(properties, ",") + `},"$defs":{` + strings.Join(defs, ",") + `}}`)
	}
	return []mcpcontract.Tool{
		{Name: "notebook_create", Input: json.RawMessage(`{"type":"object","properties":{"body":{"$ref":"#/$defs/CreateRequest"}},"$defs":{"CreateRequest":{"type":"object","properties":{"name":{"type":"string"}}}}}`), Output: reply(`{"type":"null"}`)},
		{Name: "notebook_delete", Input: mutation(), Output: reply(`{"type":"null"}`)},
		{Name: "notebook_save", Input: mutation("SaveRequest"), Output: reply(`{"$ref":"#/$defs/Notebook"}`, `"Notebook":{"type":"object","properties":{"path":{"type":"string"}}}`)},
		{Name: "notebook_save_as", Input: mutation("SaveRequest"), Output: reply(`{"$ref":"#/$defs/Notebook"}`, `"Notebook":{"type":"object","properties":{"path":{"type":"string"}}}`)},
	}
}

// declaredTypes maps each top-level type a generated client declares to its declaration.
func declaredTypes(t *testing.T, target string, tools []mcpcontract.Tool) map[string]string {
	t.Helper()
	files, err := Generate(t.Context(), GenerateRequest{
		Target: target, Identity: mcpcontract.Identity{Name: "caller", Version: "1"}, App: "caller", GoModule: "example.com/runtime",
		Contracts: map[string]*mcpcontract.Contract{"notebooks": {Tools: tools}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source, pattern := string(files["index.d.mts"]), `(?m)^export type (\w+) = .*$`
	switch target {
	case "python":
		source, pattern = string(files["__init__.py"]), `(?m)^(\w+)(?: = _typing\.TypedDict\(|: _typing\.TypeAlias = ).*$`
	case "go":
		source, pattern = string(files["types.go"]), `(?ms)^type (\w+) .*?\n\n`
	}
	declared := map[string]string{}
	for _, match := range regexp.MustCompile(pattern).FindAllStringSubmatch(source, -1) {
		declared[match[1]] = match[0]
	}
	return declared
}

// A shared type is named after what it holds, so a server that adds a tool sorting before the
// others, and using their types, leaves every name its clients wrote unchanged. A named definition
// is exported whether or not it is shared.
func TestSharedTypeNamesDoNotDependOnOtherTools(t *testing.T) {
	tools := notebookTools()
	earlier := tools[2]
	earlier.Name = "autosave"
	for _, target := range []string{"node", "python", "go"} {
		t.Run(target, func(t *testing.T) {
			before := declaredTypes(t, target, tools)
			for _, name := range []string{"NotebooksInputCreateRequest", "NotebooksInputSaveRequest", "NotebooksOutputProblem"} {
				if _, ok := before[name]; !ok {
					t.Errorf("%s is not declared; declared %v", name, slices.Sorted(maps.Keys(before)))
				}
			}
			after := declaredTypes(t, target, append([]mcpcontract.Tool{earlier}, tools...))
			for _, name := range slices.Sorted(maps.Keys(before)) {
				if after[name] != before[name] {
					t.Errorf("adding a tool changed %s from\n%s\nto\n%s", name, before[name], after[name])
				}
			}
		})
	}
}
