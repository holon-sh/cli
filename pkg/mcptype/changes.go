package mcptype

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// Changes describes each way published breaks expected if it is deployed: identity first, then tools by name,
// resources by URI and events by name, each by the first of its schemas that differs. Empty means compatible.
func Changes(expected, published *mcpcontract.Metadata) ([]string, error) {
	want, err := decodeMetadata(expected)
	if err != nil {
		return nil, err
	}
	if want == nil {
		return nil, errors.New("expected document has no schema")
	}
	var have *contract
	if published != nil {
		if have, err = decodeMetadata(published); err != nil {
			return nil, err
		}
	}
	if have == nil {
		return []string{"no published contract"}, nil
	}
	var result []string
	if expected.Identity != published.Identity {
		result = append(result, fmt.Sprintf("identity %s@%s expected, %s@%s published", expected.Identity.Name, expected.Identity.Version, published.Identity.Name, published.Identity.Version))
	}
	canonicalContract(*want)
	canonicalContract(*have)
	for _, part := range parts {
		wanted, actual := part.entries(*want), part.entries(*have)
		for _, name := range sortedKeys(wanted) {
			entry := wanted[name]
			published, ok := actual[name]
			switch {
			case !ok:
				result = append(result, fmt.Sprintf("%s %s removed", part.kind, name))
			case !reflect.DeepEqual(published.Input, entry.Input):
				result = append(result, fmt.Sprintf("%s %s %s changed", part.kind, name, part.input))
			case !reflect.DeepEqual(published.Output, entry.Output):
				result = append(result, fmt.Sprintf("%s %s %s changed", part.kind, name, part.output))
			case !reflect.DeepEqual(published.Response, entry.Response):
				result = append(result, fmt.Sprintf("%s %s response changed", part.kind, name))
			}
		}
	}
	return result, nil
}

// parts are the kinds of entry a contract types clients from, in the order Changes reports them, with
// what Changes calls each kind's Input and Output. A kind's name for a schema it never has is empty,
// and only tools have a Response. Changes and canonicalContract both walk parts, so neither leaves out
// a schema that a client is typed from.
var parts = []struct {
	kind, input, output string
	entries             func(contract) map[string]contractTool
}{
	{"tool", "input", "output", func(c contract) map[string]contractTool { return c.Tools }},
	{"resource", "", "contents", func(c contract) map[string]contractTool { return c.Resources }},
	{"event", "arguments", "data", func(c contract) map[string]contractTool { return c.Events }},
}
