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
	// Only schemas need JSON decoding; the remaining options are already typed.
	value, err := marshal(ctx, options.Contracts)
	if err != nil {
		return nil, err
	}
	request := generateRequest{GenerateRequest: options, contracts: map[string]contract{}}
	contracts, _ := value.(Object) // A nil Contracts map encodes as null.
	for alias, raw := range contracts {
		if request.contracts[alias], err = decodeContract(raw); err != nil {
			return nil, err
		}
	}
	if err := request.validate(); err != nil {
		return nil, err
	}
	// Go has no runtime that sends files, so its clients take the FileInput objects themselves.
	catalogs, err := compileCatalogs(ctx, request.contracts, request.Target != "go")
	if err != nil {
		return nil, err
	}
	var generated map[string]string
	switch request.Target {
	case "node", "browser":
		generated, err = generateTypeScriptPackage(ctx, request, catalogs)
	case "python":
		generated = generatePythonPackage(request, catalogs)
	default:
		generated = generateGoPackage(request, catalogs)
	}
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for path, text := range generated {
		if request.Banner != "" {
			comment := "//"
			if strings.HasSuffix(path, ".py") {
				comment = "#"
			}
			text = comment + " " + request.Banner + "\n" + text
		}
		files[path] = []byte(text)
	}
	return files, nil
}

func (r generateRequest) validate() error {
	if !slices.Contains([]string{"node", "browser", "python", "go"}, r.Target) {
		return fmt.Errorf("unknown target %q", r.Target)
	}
	for _, identity := range slices.AppendSeq([]mcpcontract.Identity{r.Identity}, maps.Values(r.Requires)) {
		if err := checkIdentity(identity); err != nil {
			return err
		}
	}
	if r.Banner != "" && (strings.TrimSpace(r.Banner) == "" || strings.ContainsFunc(r.Banner, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' })) {
		return errors.New("banner must be nonempty and contain no line breaks or control characters")
	}
	if r.Target == "go" && r.GoModule == "" {
		return errors.New("go_module is required for the go target")
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
