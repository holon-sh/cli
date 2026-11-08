package mcpregistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/holon-sh/cli/pkg/mcpcontract"

	wire "github.com/holon-sh/protocol"
)

const (
	PublisherMetadata  = "io.modelcontextprotocol.registry/publisher-provided"
	MaxDescriptorBytes = 1 << 20
)

type Descriptor struct {
	Icons       []wire.Icon `json:"icons,omitempty"`
	Name        string      `json:"name"`
	Title       string      `json:"title"`
	Version     string      `json:"version"`
	Description string      `json:"description"`
	WebsiteURL  string      `json:"websiteUrl"`
	Repository  struct {
		URL string `json:"url"`
	} `json:"repository"`
	Remotes  []Transport                `json:"remotes"`
	Packages []Package                  `json:"packages"`
	Meta     map[string]json.RawMessage `json:"_meta"`
	Requires map[string]Requirement     `json:"-"`
	Egress   wire.Egress                `json:"-"`
}

type HolonMetadata struct {
	Requires map[string]Requirement `description:"Dependencies keyed by app name." json:"requires"`

	Egress wire.Egress `description:"Outbound TCP destinations: DNS names or IP addresses; {NAME} is a non-secret input." json:"egress,omitzero" nullable:"false"`
}

type Requirement struct {
	wire.RegistryRef

	URL string `json:"url,omitempty"`
}

func (r Requirement) Validate() error {
	switch {
	case r.URL == "":
		return r.RegistryRef.Validate()
	case r.RegistryRef != (wire.RegistryRef{}):
		return errors.New("a url names the server; leave out name, version and registry")
	default:
		return ValidateEndpoint(r.URL)
	}
}

func ValidateEndpoint(location string) error {
	u, err := url.Parse(location)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return errors.New("MCP URL must be an HTTP(S) URL without credentials or a fragment")
	}
	return nil
}

type Package struct {
	Version          string            `json:"version"`
	RegistryType     string            `json:"registryType"`
	Identifier       string            `json:"identifier"`
	Transport        Transport         `json:"transport"`
	Environment      []json.RawMessage `json:"environmentVariables"`
	RuntimeArguments []json.RawMessage `json:"runtimeArguments"`
	PackageArguments []json.RawMessage `json:"packageArguments"`
}

type Transport struct {
	Type      string                 `json:"type"`
	URL       string                 `json:"url"`
	Headers   []DescriptorInput      `json:"headers"`
	Variables map[string]InputFields `json:"variables"`
}

func (d *Descriptor) Identity() mcpcontract.Identity {
	return mcpcontract.Identity{Name: d.Name, Version: d.Version}
}

func ParseDescriptor(data []byte) (*Descriptor, error) {
	var descriptor Descriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return nil, fmt.Errorf("decode server.json: %w", err)
	}
	if err := (wire.RegistryRef{Name: descriptor.Name, Version: descriptor.Version}).Validate(); err != nil {
		return &descriptor, err
	}
	holon, err := ParseHolonMetadata(descriptor.Meta)
	descriptor.Requires, descriptor.Egress = holon.Requires, holon.Egress
	return &descriptor, err
}

func ParseHolonMetadata(meta map[string]json.RawMessage) (HolonMetadata, error) {
	var holon HolonMetadata
	value := meta[PublisherMetadata]
	if value == nil {
		return holon, nil
	}
	var publisher map[string]json.RawMessage
	if err := json.Unmarshal(value, &publisher); err != nil {
		return holon, err
	}
	if extension := publisher["sh.holon"]; extension != nil {
		if err := wire.DecodeJSONStrict(extension, &holon); err != nil {
			return HolonMetadata{}, fmt.Errorf("sh.holon metadata: %w", err)
		}
	}
	for name, requirement := range holon.Requires {
		if !wire.ValidAppName(name) {
			return holon, fmt.Errorf("dependency name %q must produce a valid endpoint environment variable", name)
		}
		if err := requirement.Validate(); err != nil {
			return holon, fmt.Errorf("dependency %s: %w", name, err)
		}
	}
	return holon, nil
}

type InputFields wire.Input

type DescriptorInput struct {
	InputFields

	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	ValueHint string                 `json:"valueHint"`
	Variables map[string]InputFields `json:"variables"`
}

func ParseInput(raw json.RawMessage) (DescriptorInput, wire.Input, map[string]wire.Input, error) {
	var spec DescriptorInput
	if json.Unmarshal(raw, &spec) != nil {
		return spec, wire.Input{}, nil, errors.New("invalid descriptor input")
	}
	input, variables, err := spec.Parse()
	return spec, input, variables, err
}

func (spec DescriptorInput) Parse() (wire.Input, map[string]wire.Input, error) {
	input := wire.Input(spec.InputFields)
	if input.Value == nil || len(spec.Variables) == 0 {
		return input, nil, input.Validate()
	}
	variables, err := Variables(*input.Value, spec.Variables, spec.IsRequired)
	return wire.Input{Value: input.Value}, variables, err
}

func Variables(template string, declared map[string]InputFields, required bool) (map[string]wire.Input, error) {
	variables := map[string]wire.Input{}
	for _, name := range wire.TemplateInputs(template) {
		variable, ok := declared[name]
		if !ok {
			return nil, fmt.Errorf("template variable %s is undeclared", name)
		}
		variable.IsRequired = variable.IsRequired || required
		variables[name] = wire.Input(variable)
	}
	return variables, nil
}
