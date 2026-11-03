package mcpregistry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/holon-sh/cli/pkg/protocol"
)

// Binding is an input's literal value, or the secret input Env that supplies it.
type Binding struct {
	Value *string
	Env   string
}

func BindInput(input protocol.Input, value Value, supplied bool, env map[string]protocol.Input) (Binding, error) {
	var argument Binding
	argument.Value, _ = input.Source("")
	if !supplied {
		return argument, nil
	}
	if input.Value != nil {
		return argument, errors.New("fixed input cannot be overridden")
	}
	if value.Value != nil {
		if input.IsSecret {
			return argument, errors.New("input requires a secret reference, not a literal")
		}
		if !protocol.ValidInputValue(*value.Value) {
			return argument, errors.New("input must be single-line text without NUL characters")
		}
		argument.Value = value.Value
		return argument, nil
	}
	if !input.IsSecret {
		return argument, errors.New("secret references require isSecret in their declaration")
	}
	if err := mergeInput(env, value.Secret, input); err != nil {
		return argument, err
	}
	argument.Value, argument.Env = nil, value.Secret
	return argument, nil
}

func validEnvironmentName(name string) error {
	if !protocol.ValidEnvName(name) {
		return fmt.Errorf("invalid environment name %q", name)
	}
	return nil
}

func mergeInput(env map[string]protocol.Input, name string, input protocol.Input) error {
	if err := validEnvironmentName(name); err != nil {
		return err
	}
	if err := input.Validate(); err != nil {
		return err
	}
	if prior, exists := env[name]; exists {
		required := prior.IsRequired || input.IsRequired
		prior.IsRequired, input.IsRequired = false, false
		if !reflect.DeepEqual(prior, input) {
			return fmt.Errorf("input %s conflicts with environment input", name)
		}
		input.IsRequired = required
	}
	env[name] = input
	return nil
}

func mergeInputs(env, inputs map[string]protocol.Input) error {
	for name, input := range inputs {
		if err := mergeInput(env, name, input); err != nil {
			return err
		}
	}
	return nil
}

type Value struct {
	Value  *string `json:"-"`
	Secret string  `json:"secret,omitempty" minLength:"1" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" required:"true"`
}

func (a *Value) UnmarshalJSON(data []byte) error {
	if bytes.HasPrefix(data, []byte(`"`)) {
		return json.Unmarshal(data, &a.Value)
	}
	type reference Value
	if err := protocol.DecodeJSONNonNull(data, (*reference)(a)); err != nil {
		return err
	}
	if !protocol.ValidEnvName(a.Secret) {
		return errors.New("argument secret reference requires a valid secret key")
	}
	return nil
}
