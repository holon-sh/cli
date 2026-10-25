package typegen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/gofrs/flock"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/tooling"
	"io"
	"maps"
	"net/http"
	"oras.land/oras-go/v2/registry/remote/auth"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Options struct {
	Apps           []string
	Env            []string
	HTTPClient     *http.Client
	Stdout, Stderr io.Writer
	NoInstall      bool
	NoPublished    bool
	// Credential authenticates OCI registry requests ahead of the Docker credential store. A registry
	// that rejects a credential it returned (HTTP 401) fails resolution instead of leaving clients untyped.
	Credential auth.CredentialFunc
	// Extract runs one language's extractor: the runner under toolingDir/<language> reads configPath and
	// writes outputPath. It defaults to the host's Node.js or Python interpreter.
	Extract func(ctx context.Context, language, toolingDir, configPath, outputPath string) error
	// GoModule is the runtime module that generated Go clients import. When set, apps with a server.go
	// entrypoint receive a holon/ Go client package for their dependencies; their own contracts are not extracted.
	GoModule string
}

func (o Options) selects(name string) bool {
	return o.Apps == nil || slices.Contains(o.Apps, name)
}

func (o Options) defaults() Options {
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	return o
}

type preparedTypes struct {
	root, entry, language, fingerprint, diagnostic string
	metadata                                       *mcpcontract.Metadata
	packages                                       map[string]map[string]string
	sources                                        map[string]string
	usesApps                                       bool
	slot                                           slotMetadata
}

// Prepared owns temporary source snapshots. Close releases them; preparation never modifies apps.
type Prepared struct {
	Manifest Manifest
	root     string
	options  Options
	// resolved holds the contracts of non-path dependencies, by app name.
	resolved map[string]*mcpcontract.Metadata
}

func (p *Prepared) Close() error { return os.RemoveAll(p.root) }

// Metadata returns contracts by manifest app name: extracted ones for path apps, and the resolved
// image, registry or remote contracts of the dependencies that selected apps require.
func (p *Prepared) Metadata() map[string]*mcpcontract.Metadata {
	result := maps.Clone(p.resolved)
	for _, app := range p.Manifest.Apps {
		if app.prepared != nil {
			result[app.Name] = app.prepared.metadata
		}
	}
	return result
}

// Prepare snapshots local sources, resolves required contracts and generates portable client files.
func Prepare(ctx context.Context, manifest Manifest, options Options) (*Prepared, error) {
	o := options.defaults()
	var usage map[string]clientUsage
	if manifest.pathRequiresOnly {
		var err error
		usage, err = inspectClients(ctx, manifest, o)
		if err != nil {
			return nil, err
		}
	}
	manifest, err := resolveManifestGraph(ctx, manifest, &o, usage)
	if err != nil {
		return nil, err
	}
	return prepareResolved(ctx, manifest, o)
}

// prepareResolved prepares a manifest whose dependency graph and descriptors are already resolved.
func prepareResolved(ctx context.Context, manifest Manifest, o Options) (_ *Prepared, err error) {
	root, err := os.MkdirTemp("", "holon-types-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(root)
		}
	}()
	if err = tooling.Unpack(filepath.Join(root, "tooling")); err != nil {
		return nil, err
	}
	for i := range manifest.Apps {
		app := &manifest.Apps[i]
		if app.Path == "" {
			continue
		}
		p := &preparedTypes{root: filepath.Join(root, "sources", app.Name), packages: map[string]map[string]string{}}
		app.prepared = p
		if p.fingerprint, err = snapshotSource(*app, p.root); err != nil {
			return nil, err
		}
		if p.entry, p.language, err = analysisEntry(p.root); err != nil {
			return nil, err
		}
		if o.GoModule != "" && strings.HasSuffix(p.entry, ".go") {
			p.language = "go"
		}
		if p.language != "" && p.language != "typescript" {
			p.packages[p.language] = nil
		}
		if isFile(filepath.Join(p.root, "package.json")) {
			var pkg packageJSON
			if err := readTypeJSON(filepath.Join(p.root, "package.json"), &pkg); err != nil {
				return nil, err
			}
			for _, kind := range pkg.kinds(nil) {
				p.packages[kind] = nil
			}
		}
	}
	results, err := extractTypes(ctx, root, manifest.Apps, o)
	if err != nil {
		return nil, err
	}
	metadata := map[string]*mcpcontract.Metadata{}
	declared := map[string]App{}
	type resolution struct {
		metadata   *mcpcontract.Metadata
		diagnostic string
	}
	external := map[string]resolution{}
	for _, app := range manifest.Apps {
		declared[app.Name] = app
		p := app.prepared
		if p == nil {
			continue
		}
		result, ok := results[app.Name]
		if p.language == "go" {
			// Go callers are typed from their requires; their own contract is not extracted.
			result = extraction{UsesApps: app.descriptor != nil && len(app.descriptor.Requires) > 0, Diagnostic: "Go source contracts are not statically extracted; its clients are untyped"}
		}
		p.sources = result.Sources
		p.usesApps = result.UsesApps
		p.diagnostic = result.Diagnostic
		identity := mcpcontract.Identity{Name: app.Name, Version: "0.0.0"}
		if app.descriptor != nil {
			identity = app.descriptor.Identity()
		}
		p.metadata = &mcpcontract.Metadata{Version: mcpcontract.Version, Identity: identity, Requires: map[string]mcpcontract.Expectation{}}
		if ok && result.Diagnostic == "" {
			p.metadata.Contract = &mcpcontract.Contract{Tools: result.Tools, Resources: result.Resources}
		} else if p.diagnostic == "" {
			p.diagnostic = "source contract is not statically extracted; its clients are untyped"
		}
		metadata[app.Name] = p.metadata
	}
	for _, app := range manifest.Apps {
		p := app.prepared
		if p == nil || !o.selects(app.Name) {
			continue
		}
		request := mcptype.GenerateRequest{Identity: p.metadata.Identity, Requires: map[string]mcpcontract.Identity{}, Contracts: map[string]*mcpcontract.Contract{}, Banner: mcpcontract.Marker + " app=" + app.Name}
		p.slot = slotMetadata{Marker: mcpcontract.Marker, App: app.Name, Identity: p.metadata.Identity, Digests: map[string]string{}}
		if p.metadata.Contract != nil {
			canonical, err := mcptype.Canonical(ctx, map[string]any{"contract": p.metadata.Contract})
			if err != nil {
				return nil, fmt.Errorf("%s: %w", app.Name, err)
			}
			p.slot.Digests[app.Name] = canonical.Digest
		}
		if app.descriptor != nil {
			for _, name := range slices.Sorted(maps.Keys(app.descriptor.Requires)) {
				ref := app.descriptor.Requires[name]
				request.Requires[name] = ref.Identity()
				r, cached := external[name]
				if !cached {
					actual, diagnostic, err := resolveRequired(ctx, name, ref, metadata, declared, o)
					if err != nil {
						return nil, fmt.Errorf("%s typed dependency %s: %w", app.Name, name, err)
					}
					r = resolution{actual, diagnostic}
					if declared[name].Path == "" {
						external[name] = r
					}
				}
				actual, diagnostic := r.metadata, r.diagnostic
				if actual == nil || actual.Contract == nil {
					if p.usesApps {
						fmt.Fprintf(o.Stderr, "%s: typed dependency %s unavailable: %s\n", app.Name, name, diagnostic)
					}
					continue
				}
				if actual.Identity != ref.Identity() {
					return nil, fmt.Errorf("%s requires %s@%s, but %s image contract belongs to %s@%s", app.Name, ref.Name, ref.Version, name, actual.Identity.Name, actual.Identity.Version)
				}
				request.Contracts[name] = actual.Contract
				canonical, err := mcptype.Canonical(ctx, map[string]any{"metadata": actual})
				if err != nil {
					return nil, err
				}
				p.slot.Digests[name] = canonical.Digest
				if p.usesApps {
					p.metadata.Requires[name] = mcpcontract.Expectation{Identity: actual.Identity, Contract: *actual.Contract}
				}
			}
		}
		for language := range p.packages {
			files, err := generateTypes(ctx, language, request, p.metadata.Contract, o.GoModule)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", app.Name, err)
			}
			p.packages[language] = files
		}
	}
	if err = currentSources(manifest); err != nil {
		return nil, err
	}
	resolved := map[string]*mcpcontract.Metadata{}
	for name, r := range external {
		if r.metadata != nil {
			resolved[name] = r.metadata
		}
	}
	return &Prepared{Manifest: manifest, root: root, options: o, resolved: resolved}, nil
}
func generateTypes(ctx context.Context, language string, request mcptype.GenerateRequest, own *mcpcontract.Contract, goModule string) (map[string]string, error) {
	switch language {
	case "typescript":
		request.Target, request.RuntimeSpecifier, request.ProtocolSpecifier = "node", "../runtime.mjs", "../protocol.js"
	case "browser":
		// Browser views also call their own server, so its contract joins the dependencies under its
		// server key (example.com/checkout → checkout), independent of any manifest name.
		request.Target, request.BrowserRuntimeSpecifier, request.ProtocolSpecifier, request.Entry = "browser", "../runtime.mjs", "holon-core/protocol", "index"
		if own != nil {
			key := serverKey(request.Identity.Name)
			if _, taken := request.Requires[key]; taken {
				return nil, fmt.Errorf("own server %s and requires alias %s share the client name %s; rename the alias", request.Identity.Name, key, key)
			}
			request.Contracts = maps.Clone(request.Contracts)
			request.Contracts[key] = own
			request.Own = key
		}
	case "python":
		request.Target = "python"
		request.PythonModule = "holon/apps/__init__.py"
		request.PythonRuntimeModule = "holon.client"
	case "go":
		// The package matches the Holon platform's: unbannered, marked by package.json.
		request.Target = "go"
		request.Banner = ""
		request.GoModule = goModule
		request.PackageFields = map[string]any{"holon": mcpcontract.Marker}
	default:
		return nil, fmt.Errorf("unsupported language %s", language)
	}
	generated, err := mcptype.Generate(ctx, request)
	if err != nil {
		return nil, err
	}
	files := make(map[string]string, len(generated))
	for name, data := range generated {
		files[name] = string(data)
	}
	return files, nil
}
func currentSources(m Manifest) error {
	for _, app := range m.Apps {
		if p := app.prepared; p != nil {
			fingerprint, err := snapshotSource(app, "")
			if err != nil {
				return err
			}
			if fingerprint != p.fingerprint {
				return errors.New("application source changed during preparation; discarding the superseded update")
			}
		}
	}
	return nil
}

// Generate installs missing public runtimes and writes only marked generation slots.
func Generate(ctx context.Context, m Manifest, options Options) error {
	o := options.defaults()
	usage, err := inspectClients(ctx, m, o)
	if err != nil {
		return err
	}
	resolved, err := resolveManifestGraph(ctx, m, &o, usage)
	if err != nil {
		return err
	}
	targets := resolved
	targets.Apps = slices.DeleteFunc(slices.Clone(resolved.Apps), func(app App) bool { return !o.selects(app.Name) })
	release, err := acquireLocks(targets)
	if err != nil {
		return err
	}
	defer release()
	for _, app := range targets.Apps {
		if app.Path != "" {
			if err := ensureDependencies(ctx, app, usage[app.Name].Imports, o); err != nil {
				return err
			}
		}
	}
	prepared, err := prepareResolved(ctx, resolved, o)
	if err != nil {
		return err
	}
	defer prepared.Close()
	type slotKey struct{ kind, app string }
	slots := map[slotKey]string{}
	owners := map[string]string{}
	for _, app := range prepared.Manifest.Apps {
		p := app.prepared
		if p == nil || !o.selects(app.Name) {
			continue
		}
		for _, kind := range slices.Sorted(maps.Keys(slotPackages)) {
			if _, ok := p.packages[kind]; !ok {
				continue
			}
			pkg := slotPackages[kind]
			slot, _, err := nodeSlot(app.Path, pkg)
			if err != nil {
				return err
			}
			if prior, ok := owners[slot]; ok {
				return fmt.Errorf("apps %s and %s share %s; install a separate %s for each app (avoid hoisting this package)", prior, app.Name, slot, pkg)
			}
			owners[slot] = app.Name
			slots[slotKey{kind, app.Name}] = slot
			if err = validateNodeSlot(slot, pkg, p.slot); err != nil {
				return err
			}
		}
		if _, ok := p.packages["python"]; ok {
			if err := validatePythonSlot(app.Path); err != nil {
				return err
			}
		}
		if _, ok := p.packages["go"]; ok {
			if err := validateGoSlot(app.Path); err != nil {
				return err
			}
		}
	}
	if err := currentSources(prepared.Manifest); err != nil {
		return err
	}
	for _, app := range prepared.Manifest.Apps {
		p := app.prepared
		if p == nil || !o.selects(app.Name) {
			continue
		}
		for _, language := range slices.Sorted(maps.Keys(p.packages)) {
			var err error
			switch language {
			case "typescript":
				err = prepared.WriteNodeSlot(app.Name, slots[slotKey{language, app.Name}])
			case "browser":
				err = prepared.WriteBrowserSlot(app.Name, slots[slotKey{language, app.Name}])
			case "python":
				err = prepared.WritePythonSlot(app.Name, app.Path)
			case "go":
				err = prepared.WriteGoSlot(app.Name, app.Path)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "%s: generated %s types\n", app.Name, language)
		}
	}
	return nil
}

// files returns the generated files of one selected path app for one language.
func (p *Prepared) files(name, language string) (*preparedTypes, map[string]string, error) {
	for _, app := range p.Manifest.Apps {
		if app.Name != name {
			continue
		}
		if app.prepared == nil || !p.options.selects(name) {
			return nil, nil, fmt.Errorf("%s has no prepared sources; select a path app", name)
		}
		files, ok := app.prepared.packages[language]
		if !ok || files == nil {
			return nil, nil, fmt.Errorf("%s has no %s clients", name, language)
		}
		return app.prepared, files, nil
	}
	return nil, nil, fmt.Errorf("unknown app %q", name)
}

// WriteNodeSlot writes app's TypeScript clients and their holon-generated.json marker into
// pkgDir/generated, where pkgDir is an installed holon-sh package or a copy staged for an image.
// It refuses slots that are unmarked, symlinked or owned by another app, and leaves an identical slot untouched.
func (p *Prepared) WriteNodeSlot(app, pkgDir string) error {
	prepared, files, err := p.files(app, "typescript")
	if err != nil {
		return err
	}
	return writeNodeSlot(pkgDir, slotPackages["typescript"], files, prepared.slot)
}

// WriteBrowserSlot writes app's browser clients into pkgDir/generated, where pkgDir is an installed
// holon-ui package or a copy staged for an image. Same refusals as WriteNodeSlot.
func (p *Prepared) WriteBrowserSlot(app, pkgDir string) error {
	prepared, files, err := p.files(app, "browser")
	if err != nil {
		return err
	}
	return writeNodeSlot(pkgDir, slotPackages["browser"], files, prepared.slot)
}

// WritePythonSlot writes app's holon/apps/__init__.py under root, an application directory or its
// staged copy. A legacy Holon-managed holon/ package is replaced; user files in the namespace are refused.
func (p *Prepared) WritePythonSlot(app, root string) error {
	_, files, err := p.files(app, "python")
	if err != nil {
		return err
	}
	return writePythonSlot(root, files)
}

// WriteGoSlot writes app's holon/ Go client package under root. It requires Options.GoModule and
// replaces only an absent or Holon-marked holon/ directory.
func (p *Prepared) WriteGoSlot(app, root string) error {
	_, files, err := p.files(app, "go")
	if err != nil {
		return err
	}
	return writeGoSlot(root, files)
}
func acquireLocks(m Manifest) (func(), error) {
	var locks []*flock.Flock
	release := func() {
		for _, lock := range locks {
			_ = lock.Unlock()
		}
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return release, err
	}
	directory := filepath.Join(cache, "holon", "type-locks")
	if err = os.MkdirAll(directory, 0o700); err != nil {
		return release, err
	}
	paths := map[string]bool{}
	for _, app := range m.Apps {
		if app.Path != "" {
			real, err := filepath.EvalSymlinks(app.Path)
			if err != nil {
				release()
				return release, err
			}
			paths[real] = true
			for _, pkg := range slotPackages {
				if slot, _, err := nodeSlot(app.Path, pkg); err == nil {
					paths[slot] = true
				}
			}
		}
	}
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		sum := sha256.Sum256([]byte(path))
		lock := flock.New(filepath.Join(directory, hex.EncodeToString(sum[:])+".lock"))
		ok, err := lock.TryLock()
		if err != nil || !ok {
			release()
			if err == nil {
				err = fmt.Errorf("%s is already managed by another holon command; stop it before changing dependency bindings", path)
			}
			return release, err
		}
		locks = append(locks, lock)
	}
	return release, nil
}
