package mcptype

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// generateRequest retains the typed options and a private, validated schema tree.
type generateRequest struct {
	GenerateRequest
	contracts map[string]contract
}

// Generate returns the generated sources by relative path.
func Generate(ctx context.Context, options GenerateRequest) (map[string][]byte, error) {
	if err := options.validate(); err != nil {
		return nil, err
	}
	request := generateRequest{GenerateRequest: options, contracts: make(map[string]contract, len(options.Contracts))}
	for alias, source := range options.Contracts {
		if source == nil {
			return nil, fmt.Errorf("contract %s must be an object", alias)
		}
		decoded, err := decodeContract(*source)
		if err != nil {
			return nil, err
		}
		request.contracts[alias] = decoded
	}
	catalogs, err := compileCatalogs(ctx, request.contracts, request.Target != "go")
	if err != nil {
		return nil, err
	}
	var generated map[string]string
	switch request.Target {
	case "node", "browser":
		generated, err = generateTypeScriptPackage(ctx, request, catalogs)
	case "python":
		generated, err = generatePythonPackage(ctx, request, catalogs)
	default:
		generated = generateGoPackage(request, catalogs)
	}
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for path, text := range generated {
		if request.App != "" {
			comment := "//"
			if strings.HasSuffix(path, ".py") {
				comment = "#"
			}
			text = comment + " " + mcpcontract.Marker + " app=" + request.App + "\n" + text
		}
		files[path] = []byte(text)
	}
	return files, nil
}

func eventContracts(contracts map[string]contract) map[string]contract {
	result := make(map[string]contract, len(contracts))
	for alias, source := range contracts {
		tools := make(map[string]contractTool, len(source.Events))
		for name, event := range source.Events {
			tools[name] = contractTool{Input: event.Input, Output: event.Payload}
		}
		result[alias] = contract{Tools: tools}
	}
	return result
}

func (r GenerateRequest) validate() error {
	if !slices.Contains([]string{"node", "browser", "python", "go"}, r.Target) {
		return fmt.Errorf("unknown target %q", r.Target)
	}
	for _, identity := range slices.AppendSeq([]mcpcontract.Identity{r.Identity}, maps.Values(r.Requires)) {
		if err := checkIdentity(identity); err != nil {
			return err
		}
	}
	if r.App != "" && (strings.TrimSpace(r.App) == "" || strings.ContainsFunc(r.App, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' })) {
		return errors.New("app must be nonempty and contain no line breaks or control characters")
	}
	if r.Target == "go" && r.GoModule == "" {
		return errors.New("go_module is required for the go target")
	}
	if _, empty := r.Contracts[""]; empty {
		return errors.New("binding names must be nonempty")
	}
	return nil
}

// caller is the identity a generated client announces, with the aliases its app declares.
func (r generateRequest) caller() Object {
	requires := make(Object, len(r.Requires))
	for alias, identity := range r.Requires {
		requires[alias] = identity
	}
	return Object{"name": r.Identity.Name, "requires": requires, "version": r.Identity.Version}
}
