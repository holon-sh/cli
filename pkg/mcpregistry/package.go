package mcpregistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/holon-sh/cli/pkg/protocol"
)

type RunConfig struct {
	Args []string
	Env  map[string]Value
}

// SelectServerSource picks the one OCI, npm or PyPI package or Streamable HTTP remote that source allows. It does
// not validate the choice: callers check the selected package for what they use it for.
func SelectServerSource(d *Descriptor, source protocol.RegistrySource) (int, *Transport, error) {
	if source.Package != nil && source.Remote != nil {
		return -1, nil, errors.New("registry.package and registry.remote are mutually exclusive")
	}
	selected := ptr.Deref(source.Package, protocol.PackageSelection{})
	if source.Package != nil && !supportedRegistryType(selected.RegistryType) {
		return -1, nil, errors.New("registryType must be oci, npm or pypi")
	}
	index, count := -1, 0
	var remote *Transport
	if source.Remote == nil {
		for i, pkg := range d.Packages {
			if supportedRegistryType(pkg.RegistryType) && (selected.RegistryType == "" || selected.RegistryType == pkg.RegistryType) &&
				(selected.Identifier == "" || selected.Identifier == pkg.Identifier) {
				index = i
				count++
			}
		}
	}
	if source.Package == nil {
		for i, r := range d.Remotes {
			if r.Type == "streamable-http" {
				remote = &d.Remotes[i]
				count++
			}
		}
	}
	if count > 1 {
		return -1, nil, errors.New("ambiguous server deployment; select registry.remote or registry.package with its published identifier")
	}
	if count == 0 {
		return -1, nil, errors.New("server requires an OCI, npm or PyPI package, or a Streamable HTTP remote")
	}
	return index, remote, nil
}

// environmentInputs returns the package's environment inputs and the variables it sets from templates over them.
func (p Package) environmentInputs() (map[string]protocol.Input, map[string]string, error) {
	inputs, templates := map[string]protocol.Input{}, map[string]string{}
	set := func(name string, input protocol.Input, variables map[string]protocol.Input) error {
		if err := mergeInputs(inputs, variables); err != nil {
			return err
		}
		if variables == nil {
			return mergeInput(inputs, name, input)
		}
		templates[name] = *input.Value
		return validEnvironmentName(name)
	}
	for _, raw := range p.Environment {
		spec, input, variables, err := ParseInput(raw)
		if err != nil {
			return nil, nil, err
		}
		if err := set(spec.Name, input, variables); err != nil {
			return nil, nil, err
		}
	}
	if slices.Contains([]string{"npm", "pypi"}, p.RegistryType) {
		return inputs, templates, nil
	}
	// The other runtime arguments configure a local docker run, which Holon replaces.
	for _, raw := range p.RuntimeArguments {
		var spec DescriptorInput
		err := json.Unmarshal(raw, &spec)
		name, value, assigns := strings.Cut(ptr.Deref(spec.Value, ""), "=")
		if err != nil || spec.Name != "-e" && spec.Name != "--env" || !assigns {
			continue
		}
		assigned := DescriptorInput{
			InputFields: InputFields{Value: &value, IsRequired: spec.IsRequired}, Variables: spec.Variables,
		}
		input, variables, err := assigned.Parse()
		if err == nil {
			err = set(name, input, variables)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return inputs, templates, nil
}

// ValidateTransport checks how the package serves MCP. The CLI needs only this: it builds a local app's image from
// source, parses a Registry OCI identifier when it copies the image, and the server checks npm and PyPI sources.
func (p Package) ValidateTransport() error {
	if _, err := p.Port(); err != nil {
		return err
	}
	if len(p.Transport.Headers) != 0 {
		return errors.New("package HTTP headers are unsupported")
	}
	_, _, err := p.environmentInputs()
	return err
}

func (p Package) Port() (int, error) {
	if p.Transport.Type == "stdio" {
		return 3000, nil
	}
	if p.Transport.Type != "streamable-http" {
		return 0, errors.New("package transport.type must be stdio or streamable-http")
	}
	// Any host: Holon reaches the package on its own pod. The URL names the package's published values.
	raw, _ := protocol.Expand(p.Transport.URL, p.publishedValue)
	if u, err := url.Parse(raw); err == nil && raw == "http://"+u.Host+"/mcp" {
		if port, _ := strconv.Atoi(u.Port()); protocol.ValidPort(port) {
			return port, nil
		}
	}
	return 0, errors.New("package transport.url must be http://HOST:PORT/mcp with a port between 1 and 65535")
}

// publishedValue is the value or default the package publishes for an environment variable or an argument, which
// transport.url names by its name or value hint.
func (p Package) publishedValue(name string) (string, bool) {
	for _, raw := range slices.Concat(p.Environment, p.PackageArguments) {
		spec, input, _, _ := ParseInput(raw)
		if spec.Name == name || spec.ValueHint == name {
			value, _ := input.Source("")
			return ptr.Deref(value, ""), value != nil
		}
	}
	return "", false
}

// App returns the package's inputs, its environment templates and the container arguments its packageArguments and
// run.args render to. A configurable argument becomes an input named after it, and values bind those inputs by
// argument name: a literal fixes the input, and a secret reference must name the input itself.
func (p Package) App(values map[string]Value, run RunConfig) (protocol.App, error) {
	env, templates, err := p.environmentInputs()
	if err != nil {
		return protocol.App{}, err
	}
	args := []protocol.Template{}
	used := map[string]bool{}
	for index, raw := range p.PackageArguments {
		spec, input, variables, err := ParseInput(raw)
		if err == nil {
			err = mergeInputs(env, variables)
		}
		if err != nil {
			return protocol.App{}, err
		}
		id := spec.ValueHint
		switch spec.Type {
		case "named":
			if spec.Name == "" || !protocol.ValidInputValue(spec.Name) {
				return protocol.App{}, errors.New("named package argument requires a valid name")
			}
			id = spec.Name
		case "positional":
			if !protocol.ValidInputValue(id) || input.Value == nil && id == "" {
				return protocol.App{}, errors.New("configurable positional argument requires valueHint")
			}
		default:
			return protocol.App{}, errors.New("package argument type must be named or positional")
		}
		separate := spec.Type == "named" && !strings.HasPrefix(spec.Name, "--")
		value := ptr.Deref(input.Value, "")
		if input.Value == nil {
			key := argumentInput(id)
			if _, exists := env[key]; exists || !protocol.ValidEnvName(key) {
				return protocol.App{}, fmt.Errorf("argument %s must convert to a unique environment name, not %q", id, key)
			}
			used[id] = true
			input.IsRequired = input.IsRequired || separate || spec.Type == "positional" && index != len(p.PackageArguments)-1
			if err := mergeInput(env, key, input); err != nil {
				return protocol.App{}, fmt.Errorf("argument %s: %w", id, err)
			}
			if bound, supplied := values[id]; supplied {
				binding, err := BindInput(input, bound, true, env)
				if err == nil && binding.Env != "" && binding.Env != key {
					err = fmt.Errorf("secret reference must name the input %s", key)
				}
				if err != nil {
					return protocol.App{}, fmt.Errorf("argument %s: %w", id, err)
				}
				if binding.Value != nil {
					env[key] = protocol.Input{Value: binding.Value}
				}
			}
			value = "{" + key + "}"
		}
		switch {
		case spec.Type == "positional":
			args = append(args, value)
		case separate:
			args = append(args, spec.Name, value)
		default:
			args = append(args, spec.Name+"="+value)
		}
	}
	for name, value := range run.Env {
		if value.Secret != "" && value.Secret != name {
			return protocol.App{}, fmt.Errorf("run.env.%s secret key must match the environment name", name)
		}
		input := protocol.Input{Value: value.Value, IsSecret: value.Secret != "", IsRequired: value.Secret != ""}
		if err := mergeInput(env, name, input); err != nil {
			return protocol.App{}, fmt.Errorf("run.env.%s: %w", name, err)
		}
	}
	args = append(args, run.Args...)
	for id := range values {
		if !used[id] {
			return protocol.App{}, fmt.Errorf("unknown argument %s", id)
		}
	}
	if !protocol.ValidArgs(env, args) || !protocol.ValidEnvTemplates(env, templates) {
		return protocol.App{}, errors.New("package args and environment templates must be single-line text naming declared inputs")
	}
	return protocol.App{Env: env, EnvTemplates: templates, Args: args}, nil
}

// argumentInput names the input of a configurable argument after its name or value hint: --api-key and apiKey become
// API_KEY.
func argumentInput(id string) string {
	return strings.Trim(nonAlphanumeric.ReplaceAllString(strings.ToUpper(camelBoundary.ReplaceAllString(id, "${1}_${2}")), "_"), "_")
}

var (
	camelBoundary   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	nonAlphanumeric = regexp.MustCompile(`[^A-Z0-9]+`)
)

func supportedRegistryType(registryType string) bool {
	return slices.Contains([]string{"oci", "npm", "pypi"}, registryType)
}
