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
		pages    []json.RawMessage
		rules    map[string]protocol.AccessRule
		findings []Finding
	}{
		{
			"effects and rules",
			[]json.RawMessage{
				json.RawMessage(`{"tools":[{"name":"get",` + input + `,"annotations":{"readOnlyHint":true}},{"name":"add",` + input + `,"annotations":{"destructiveHint":false}}]}`),
				json.RawMessage(`{"tools":[{"name":"run",` + input + `}]}`),
			},
			map[string]protocol.AccessRule{"get": {}, "remove": {}},
			[]Finding{{RuleUndeclaredEffect, "run"}, {RuleUnlistedTool, "remove"}},
		},
		{"listed twice", []json.RawMessage{json.RawMessage(`{"tools":[{"name":"get",` + input + `}]}`), json.RawMessage(`{"tools":[{"name":"get",` + input + `}]}`)}, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			served := &Served{Lists: map[string]ServedList{"tools/list": {Pages: test.pages}}}
			findings, err := served.Check(test.rules)
			if (err != nil) == (test.findings != nil) || !reflect.DeepEqual(findings, test.findings) {
				t.Fatalf("Check = %v, %v; want %v", findings, err, test.findings)
			}
		})
	}
}
