package mcpregistry

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/holon-sh/cli/pkg/protocol"
)

func (t Transport) Remote(values map[string]Value) (*protocol.Remote, map[string]protocol.Input, error) {
	env, err := Variables(t.URL, t.Variables, true)
	if err != nil {
		return nil, nil, err
	}
	supplied := make(map[string]Value, len(values))
	for name, value := range values {
		canonical := http.CanonicalHeaderKey(name)
		if _, exists := supplied[canonical]; exists {
			return nil, nil, fmt.Errorf("duplicate header %s", canonical)
		}
		supplied[canonical] = value
	}
	remote := &protocol.Remote{URL: t.URL, Headers: map[string]string{}}
	seen := map[string]bool{}
	for _, spec := range t.Headers {
		input, variables, err := spec.Parse()
		if err == nil {
			err = mergeInputs(env, variables)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("header %s: %w", spec.Name, err)
		}
		name := http.CanonicalHeaderKey(spec.Name)
		if seen[name] {
			return nil, nil, errors.New("duplicate remote header")
		}
		seen[name] = true
		binding, err := bindInput(input, supplied[name], env)
		delete(supplied, name)
		switch {
		case err != nil:
			return nil, nil, fmt.Errorf("header %s: %w", name, err)
		case binding.env != "":
			remote.Headers[name] = "{" + binding.env + "}"
		case binding.value != nil:
			remote.Headers[name] = *binding.value
		case input.IsRequired:
			return nil, nil, fmt.Errorf("header %s requires a value or secret reference in apps[].headers", name)
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
