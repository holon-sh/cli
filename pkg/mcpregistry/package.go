package mcpregistry

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/holon-sh/protocol"
)

type RunConfig struct {
	Args []string
	Env  map[string]Value
}

// SelectPackage returns the index of the one OCI, npm or PyPI package in d that matches selection,
// whose empty fields match any package.
func SelectPackage(d *Descriptor, selection protocol.PackageSelection) (int, error) {
	if selection.RegistryType != "" && !supportedRegistryType(selection.RegistryType) {
		return -1, errors.New("registryType must be oci, npm or pypi")
	}
	index, count := -1, 0
	for i, pkg := range d.Packages {
		if supportedRegistryType(pkg.RegistryType) && (selection.RegistryType == "" || selection.RegistryType == pkg.RegistryType) &&
			(selection.Identifier == "" || selection.Identifier == pkg.Identifier) {
			index = i
			count++
		}
	}
	var remotes []string
	for _, remote := range d.Remotes {
		if _, err := protocol.RemoteURL(remote.URL); err == nil && remote.Type == "streamable-http" {
			remotes = append(remotes, remote.URL)
		}
	}
	slices.Sort(remotes)
	switch remotes = slices.Compact(remotes); {
	case count > 1:
		return -1, errors.New("ambiguous package; select one with registry.package and its published identifier")
	case count == 0 && len(remotes) != 0:
		return -1, fmt.Errorf("server publishes no matching OCI, npm or PyPI package; for its remote endpoint, declare the app with remote.url %s", strings.Join(remotes, " or "))
	case count == 0:
		return -1, errors.New("server publishes no matching OCI, npm or PyPI package")
	}
	return index, nil
}

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

func (p Package) ValidateTransport() error {
	if _, err := p.Port(); err != nil {
		return err
	}
	if len(p.Transport.Headers) != 0 {
		return errors.New("package HTTP headers are unsupported")
	}
	return nil
}

func (p Package) Port() (int, error) {
	if p.Transport.Type == "stdio" {
		return 3000, nil
	}
	if p.Transport.Type != "streamable-http" {
		return 0, errors.New("package transport.type must be stdio or streamable-http")
	}
	raw, _ := protocol.Expand(p.Transport.URL, p.publishedValue)
	if u, err := url.Parse(raw); err == nil && raw == "http://"+u.Host+"/mcp" {
		if port, _ := strconv.Atoi(u.Port()); protocol.ValidPort(port) {
			return port, nil
		}
	}
	return 0, errors.New("package transport.url must be http://HOST:PORT/mcp with a port between 1 and 65535")
}

func (p Package) publishedValue(name string) (string, bool) {
	for _, raw := range slices.Concat(p.Environment, p.PackageArguments) {
		spec, input, _, _ := ParseInput(raw)
		if spec.Name == name || spec.ValueHint == name {
			value := cmp.Or(input.Value, input.Default)
			return ptr.Deref(value, ""), value != nil
		}
	}
	return "", false
}

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
				binding, err := bindInput(input, bound, env)
				if err == nil && binding.env != "" && binding.env != key {
					err = fmt.Errorf("secret reference must name the input %s", key)
				}
				if err != nil {
					return protocol.App{}, fmt.Errorf("argument %s: %w", id, err)
				}
				if binding.value != nil {
					env[key] = protocol.Input{Value: binding.value}
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
