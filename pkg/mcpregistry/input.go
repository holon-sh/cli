package mcpregistry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/holon-sh/protocol"
)

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
