package cloud

import (
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/protocol"
)

// remoteInputs reads a remote and binds each header without a published value to its literal or secret reference in
// values.
func remoteInputs(transport serverTransport, values map[string]mcpregistry.Value) (*protocol.Remote, map[string]protocol.Input, error) {
	remote, env, headers, err := mcpregistry.RemoteInputs(transport)
	if err != nil {
		return nil, nil, err
	}
	supplied := map[string]mcpregistry.Value{}
	for name, value := range values {
		canonical := http.CanonicalHeaderKey(name)
		if _, exists := supplied[canonical]; exists {
			return nil, nil, fmt.Errorf("duplicate header %s", canonical)
		}
		supplied[canonical] = value
	}
	for _, header := range headers {
		value, present := supplied[header.Name]
		delete(supplied, header.Name)
		argument, err := mcpregistry.BindInput(header.Input, value, present, env)
		switch {
		case err != nil:
			return nil, nil, fmt.Errorf("header %s: %w", header.Name, err)
		case argument.Env != "":
			remote.Headers[header.Name] = "{" + argument.Env + "}"
		case argument.Value != nil:
			remote.Headers[header.Name] = *argument.Value
		case header.Input.IsRequired:
			return nil, nil, fmt.Errorf("header %s requires a value or secret reference in apps[].headers", header.Name)
		}
	}
	if len(supplied) != 0 {
		return nil, nil, fmt.Errorf("unknown headers %v", slices.Sorted(maps.Keys(supplied)))
	}
	if err := remote.Validate(env); err != nil {
		return nil, nil, err
	}
	return remote, env, nil
}
