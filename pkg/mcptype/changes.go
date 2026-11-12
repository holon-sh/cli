package mcptype

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// Change is one way a published contract breaks an expectation.
type Change struct {
	Text string
}

// Changes lists what breaks expected if published is deployed: identity first, then tools by name,
// then resources by URI. Empty means compatible.
func Changes(ctx context.Context, expected, published *mcpcontract.Metadata) ([]Change, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
		return []Change{{Text: "no published contract"}}, nil
	}
	var result []Change
	if expected.Identity != published.Identity {
		result = append(result, Change{Text: fmt.Sprintf("identity %s@%s expected, %s@%s published", expected.Identity.Name, expected.Identity.Version, published.Identity.Name, published.Identity.Version)})
	}
	canonicalContract(*want)
	canonicalContract(*have)
	for _, name := range sortedKeys(want.Tools) {
		tool := want.Tools[name]
		actual, ok := have.Tools[name]
		switch {
		case !ok:
			result = append(result, Change{Text: fmt.Sprintf("tool %s removed", name)})
		case !reflect.DeepEqual(actual.Input, tool.Input):
			result = append(result, Change{Text: fmt.Sprintf("tool %s input changed", name)})
		case !reflect.DeepEqual(actual.Output, tool.Output):
			result = append(result, Change{Text: fmt.Sprintf("tool %s output changed", name)})
		}
	}
	for _, uri := range sortedKeys(want.Resources) {
		actual, ok := have.Resources[uri]
		switch {
		case !ok:
			result = append(result, Change{Text: fmt.Sprintf("resource %s removed", uri)})
		case !reflect.DeepEqual(actual.Output, want.Resources[uri].Output):
			result = append(result, Change{Text: fmt.Sprintf("resource %s contents changed", uri)})
		}
	}
	return result, nil
}
