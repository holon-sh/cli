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
	contracts map[string]Contract
}

// generate lays out one target's package: copied runtime files plus the generated sources.
func generate(ctx context.Context, request generateRequest) (map[string][]byte, error) {
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
	for _, option := range []struct {
		name  string
		value string
	}{{"runtime_specifier", r.RuntimeSpecifier}, {"browser_runtime_specifier", r.BrowserRuntimeSpecifier}, {"protocol_specifier", r.ProtocolSpecifier}, {"banner", r.Banner}} {
		if option.value == "" {
			continue
		}
		if strings.TrimSpace(option.value) == "" || strings.ContainsFunc(option.value, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }) {
			return fmt.Errorf("%s must be nonempty and contain no line breaks or control characters", option.name)
		}
	}
	if entry := r.Entry; entry != "" && (!relative(entry) || strings.ContainsAny(entry, "/\\:") || strings.ContainsFunc(entry, unicode.IsControl)) {
		return errors.New("entry must be a nonempty file name without separators or control characters")
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

// untyped lists declared aliases without a contract: their clients call tools by name, unvalidated.
func (r generateRequest) untyped() []string {
	var aliases []string
	for _, alias := range sortedKeys(r.Requires) {
		if _, ok := r.contracts[alias]; !ok {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

// writeCaller is the identity a generated client announces, with the aliases its app declares.
func (r generateRequest) writeCaller(out *bytes.Buffer) {
	requires := make(Object, len(r.Requires))
	for alias, identity := range r.Requires {
		requires[alias] = Object{"name": identity.Name, "version": identity.Version}
	}
	writeValue(out, Object{"name": r.Identity.Name, "requires": requires, "version": r.Identity.Version})
}

func relative(path string) bool {
	return path != "." && fs.ValidPath(path)
}
