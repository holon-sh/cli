package typegen

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/holon-sh/protocol"
)

func TestCheck(t *testing.T) {
	const input = `"inputSchema":{"type":"object"}`
	for _, test := range []struct {
		name     string
		pages    []string
		rules    []string
		findings []Finding
	}{
		{
			"effects and rules",
			[]string{`{"tools":[{"name":"get",` + input + `,"annotations":{"readOnlyHint":true}},{"name":"add",` + input + `,"annotations":{"destructiveHint":false}}]}`,
				`{"tools":[{"name":"run",` + input + `}]}`},
			[]string{"get", "remove"},
			[]Finding{{RuleUnlistedTool, "remove"}, {RuleUndeclaredEffect, "run"}},
		},
		{"input schema of another type", []string{`{"tools":[{"name":"get","inputSchema":{"type":"string"}}]}`}, nil, nil},
		{"invalid name", []string{`{"tools":[{"name":"get\n",` + input + `}]}`}, nil, nil},
		{"listed twice", []string{`{"tools":[{"name":"get",` + input + `}]}`, `{"tools":[{"name":"get",` + input + `}]}`}, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			served := &Served{Lists: map[string]ServedList{"tools/list": {}}}
			for _, page := range test.pages {
				list := served.Lists["tools/list"]
				list.Pages = append(list.Pages, json.RawMessage(page))
				served.Lists["tools/list"] = list
			}
			rules := map[string]protocol.AccessRule{}
			for _, name := range test.rules {
				rules[name] = protocol.AccessRule{}
			}
			findings, err := served.Check(rules)
			if test.findings == nil && err == nil {
				t.Fatalf("Check accepted %v", test.pages)
			}
			if test.findings != nil && (err != nil || !reflect.DeepEqual(findings, test.findings)) {
				t.Fatalf("Check = %v, %v; want %v", findings, err, test.findings)
			}
		})
	}
}
