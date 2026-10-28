package mcptype

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
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

// Generate returns the package files by relative path: copied runtime files plus generated sources.
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

	for _, path := range sortedKeys(request.Runtime) {
		if !relative(path) {
			return nil, fmt.Errorf("runtime path %q must be relative and normalized", path)
		}
		if request.Runtime[path] == nil {
			return nil, fmt.Errorf("runtime %s must contain bytes", path)
		}
	}
	if err := request.validate(); err != nil {
		return nil, err
	}
	catalogs, err := compileCatalogs(ctx, request.contracts)
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
	for path, data := range request.Runtime {
		files[path] = bytes.Clone(data)
	}
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
		if identity.Name == "" || identity.Version == "" {
			return errors.New("identity name and version must be nonempty")
		}
	}
	if r.Protocol != "" && r.ProtocolSpecifier != "" {
		return errors.New("protocol and protocol_specifier are mutually exclusive")
	}
	if r.Own != "" {
		if _, ok := r.contracts[r.Own]; r.Target != "browser" || !ok {
			return errors.New("own must name a contract of a browser client")
		}
	}
	for _, option := range []struct {
		name  string
		value string
	}{{"runtime_specifier", r.RuntimeSpecifier}, {"protocol_specifier", r.ProtocolSpecifier}, {"banner", r.Banner}} {
		if option.value == "" {
			continue
		}
		if strings.TrimSpace(option.value) == "" || strings.ContainsFunc(option.value, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }) {
			return fmt.Errorf("%s must be nonempty and contain no line breaks or control characters", option.name)
		}
	}
	if path := r.PythonModule; path != "" && (!relative(path) || strings.ContainsAny(path, "\\:") || strings.ContainsFunc(path, unicode.IsControl) || !strings.HasSuffix(path, ".py")) {
		return errors.New("python_module must be a relative, normalized .py path")
	}
	if module := r.PythonRuntimeModule; module != "" {
		for _, part := range strings.Split(module, ".") {
			if !pythonName.MatchString(part) || slices.Contains(pythonKeywords, part) {
				return errors.New("python_runtime_module must be an absolute Python module name")
			}
		}
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

func relative(path string) bool {
	return path != "." && fs.ValidPath(path)
}
