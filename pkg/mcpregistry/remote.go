package mcpregistry

import (
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/holon-sh/cli/pkg/protocol"
)

// RemoteInputs reads a Registry remote. Its URL and each header value the publisher sets are templates over the
// variables they declare, which become the remote's inputs. It returns every header in order; one without a value is
// left for the deployer to bind.
func RemoteInputs(transport Transport) (*protocol.Remote, map[string]protocol.Input, []protocol.RegistryHeader, error) {
	remote := &protocol.Remote{URL: transport.URL, Headers: map[string]string{}}
	env, err := Variables(transport.URL, transport.Variables, true)
	if err != nil {
		return nil, nil, nil, err
	}
	headers := []protocol.RegistryHeader{}
	for _, spec := range transport.Headers {
		input, variables, err := spec.Parse()
		if err == nil {
			err = mergeInputs(env, variables)
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("header %s: %w", spec.Name, err)
		}
		name := http.CanonicalHeaderKey(spec.Name)
		if slices.ContainsFunc(headers, func(header protocol.RegistryHeader) bool { return header.Name == name }) {
			return nil, nil, nil, errors.New("duplicate remote header")
		}
		if input.Value != nil {
			remote.Headers[name] = *input.Value
		}
		headers = append(headers, protocol.RegistryHeader{Name: name, Input: input})
	}
	return remote, env, headers, nil
}
