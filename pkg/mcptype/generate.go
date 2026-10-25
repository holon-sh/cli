package mcptype

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// generateRequest is one target's package request, decoded and validated.
type generateRequest struct {
	target    string
	identity  Identity
	requires  map[string]Identity // every declared dependency alias, whether or not it has a contract
	contracts map[string]Contract // the typed dependencies for this target, by alias
	runtime   map[string][]byte   // files copied into the package verbatim
	// Node and browser: protocol declarations spliced into each .d.ts entry point.
	protocol *string
	// Import the installed runtime instead of a copied runtime file.
	runtimeSpecifier, browserRuntimeSpecifier *string
	// Import protocol declarations instead of inlining protocol.
	protocolSpecifier *string
	// Python: normalized relative output path instead of _apps.py, and the absolute runtime module instead of .client.
	pythonModule, pythonRuntimeModule *string
	// First-line comment on generated source files (not JSON or copied runtime files).
	banner *string
	// Extra package.json fields.
	packageFields Object
	// Go: import path of the runtime module.
	goModule *string
	// Node and browser: base name of the entry point files (<entry>.js, <entry>.d.ts) instead of index or browser.
	entry *string
	// Browser: the contracts key of the app's own server, reached through the MCP Apps host.
	own *string
}

var generateFields = []string{"target", "identity", "requires", "contracts", "runtime", "protocol", "runtime_specifier", "browser_runtime_specifier", "protocol_specifier", "python_module", "python_runtime_module", "banner", "package_fields", "go_module", "entry", "own"}

func decodeGenerateRequest(value Value) (generateRequest, error) {
	object, err := asObject(value)
	if err != nil {
		return generateRequest{}, err
	}
	for _, key := range sortedKeys(object) {
		if !slices.Contains(generateFields, key) {
			return generateRequest{}, fmt.Errorf("unknown field `%s`", key)
		}
	}
	request := generateRequest{requires: map[string]Identity{}, contracts: map[string]Contract{}, runtime: map[string][]byte{}, packageFields: Object{}}
	raw, err := requiredField(object, "target")
	if err != nil {
		return generateRequest{}, err
	}
	if request.target, err = asString(raw); err != nil {
		return generateRequest{}, err
	}
	if !slices.Contains([]string{"node", "browser", "python", "go"}, request.target) {
		return generateRequest{}, fmt.Errorf("unknown variant `%s`, expected one of `node`, `browser`, `python`, `go`", request.target)
	}
	if raw, err = requiredField(object, "identity"); err != nil {
		return generateRequest{}, err
	}
	if request.identity, err = decodeIdentity(raw); err != nil {
		return generateRequest{}, err
	}
	if raw, ok := object["requires"]; ok {
		requires, err := asObject(raw)
		if err != nil {
			return generateRequest{}, err
		}
		for alias, item := range requires {
			if request.requires[alias], err = decodeIdentity(item); err != nil {
				return generateRequest{}, err
			}
		}
	}
	if raw, ok := object["contracts"]; ok {
		contracts, err := asObject(raw)
		if err != nil {
			return generateRequest{}, err
		}
		for alias, item := range contracts {
			if request.contracts[alias], err = decodeContract(item); err != nil {
				return generateRequest{}, err
			}
		}
	}
	if raw, ok := object["runtime"]; ok {
		runtime, err := asObject(raw)
		if err != nil {
			return generateRequest{}, err
		}
		for path, item := range runtime {
			text, err := asString(item)
			if err != nil {
				return generateRequest{}, err
			}
			if request.runtime[path], err = base64.StdEncoding.DecodeString(text); err != nil {
				return generateRequest{}, fmt.Errorf("runtime %s: %w", path, err)
			}
		}
	}
	for name, target := range map[string]**string{
		"protocol": &request.protocol, "runtime_specifier": &request.runtimeSpecifier, "browser_runtime_specifier": &request.browserRuntimeSpecifier,
		"protocol_specifier": &request.protocolSpecifier, "python_module": &request.pythonModule, "python_runtime_module": &request.pythonRuntimeModule,
		"banner": &request.banner, "go_module": &request.goModule, "entry": &request.entry, "own": &request.own,
	} {
		if *target, err = optionalString(object, name); err != nil {
			return generateRequest{}, err
		}
	}
	if raw, ok := object["package_fields"]; ok {
		if request.packageFields, err = asObject(raw); err != nil {
			return generateRequest{}, err
		}
	}
	return request, nil
}

// generate lays out one target's package: copied runtime files plus the generated sources.
func generate(ctx context.Context, request generateRequest) (map[string][]byte, error) {
	for _, path := range sortedKeys(request.runtime) {
		if !relative(path) {
			return nil, fmt.Errorf("runtime path %q must be relative and normalized", path)
		}
	}
	if err := request.validate(); err != nil {
		return nil, err
	}
	var generated map[string]string
	var err error
	switch request.target {
	case "node", "browser":
		generated, err = generateTypeScriptPackage(ctx, request)
	case "python":
		generated, err = generatePythonPackage(ctx, request)
	default:
		generated, err = generateGoPackage(ctx, request)
	}
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(request.runtime)+len(generated))
	for path, data := range request.runtime {
		files[path] = data
	}
	for path, text := range generated {
		if request.banner != nil && path != "package.json" {
			comment := "//"
			if strings.HasSuffix(path, ".py") {
				comment = "#"
			}
			text = comment + " " + *request.banner + "\n" + text
		}
		files[path] = []byte(text)
	}
	return files, nil
}

func (r generateRequest) validate() error {
	if r.protocol != nil && r.protocolSpecifier != nil {
		return errors.New("protocol and protocol_specifier are mutually exclusive")
	}
	for _, option := range []struct {
		name  string
		value *string
	}{{"runtime_specifier", r.runtimeSpecifier}, {"browser_runtime_specifier", r.browserRuntimeSpecifier}, {"protocol_specifier", r.protocolSpecifier}, {"banner", r.banner}} {
		if option.value == nil {
			continue
		}
		if strings.TrimSpace(*option.value) == "" || strings.ContainsFunc(*option.value, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }) {
			return fmt.Errorf("%s must be nonempty and contain no line breaks or control characters", option.name)
		}
	}
	if entry := r.entry; entry != nil && (!relative(*entry) || strings.ContainsAny(*entry, "/\\:") || strings.ContainsFunc(*entry, unicode.IsControl)) {
		return errors.New("entry must be a nonempty file name without separators or control characters")
	}
	if own := r.own; own != nil {
		if _, ok := r.contracts[*own]; !ok {
			return errors.New("own must name one of the contracts")
		}
	}
	if path := r.pythonModule; path != nil && (!relative(*path) || strings.ContainsAny(*path, "\\:") || strings.ContainsFunc(*path, unicode.IsControl) || !strings.HasSuffix(*path, ".py")) {
		return errors.New("python_module must be a relative, normalized .py path")
	}
	if module := r.pythonRuntimeModule; module != nil {
		for _, part := range strings.Split(*module, ".") {
			if !pythonIdentifier(part) && !privatePythonIdentifier(part) {
				return errors.New("python_runtime_module must be an absolute Python module name")
			}
		}
	}
	return nil
}

func privatePythonIdentifier(part string) bool {
	if !strings.HasPrefix(part, "_") {
		return false
	}
	for i := 0; i < len(part); i++ {
		if c := part[i]; !isASCIIAlphanumeric(c) && c != '_' {
			return false
		}
	}
	return true
}

// slot reports whether the package fills an installed package's generation slot instead of being one itself.
func (r generateRequest) slot() bool {
	return r.runtimeSpecifier != nil || r.browserRuntimeSpecifier != nil || r.pythonModule != nil
}

// untyped lists declared aliases without a contract: their clients call tools by name, unvalidated.
func (r generateRequest) untyped() []string {
	var aliases []string
	for _, alias := range sortedKeys(r.requires) {
		if _, ok := r.contracts[alias]; !ok {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

// writeCaller is the identity a generated client announces, with the aliases its app declares.
func (r generateRequest) writeCaller(out *bytes.Buffer) {
	out.WriteString(`{"name":`)
	writeQuoted(out, r.identity.Name)
	out.WriteString(`,"requires":{`)
	for i, alias := range sortedKeys(r.requires) {
		if i > 0 {
			out.WriteByte(',')
		}
		writeQuoted(out, alias)
		out.WriteByte(':')
		r.requires[alias].writeWire(out)
	}
	out.WriteString(`},"version":`)
	writeQuoted(out, r.identity.Version)
	out.WriteByte('}')
}

func relative(path string) bool {
	if strings.HasPrefix(path, "/") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
