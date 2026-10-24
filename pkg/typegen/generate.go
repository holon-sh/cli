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
	"os"
	"path/filepath"
	"slices"
	"time"
)

type Options struct {
	Apps           []string
	Env            []string
	HTTPClient     *http.Client
	Stdout, Stderr io.Writer
	NoInstall      bool
	NoPublished    bool
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
}

func (p *Prepared) Close() error { return os.RemoveAll(p.root) }

// Metadata returns extracted contracts by manifest app name.
func (p *Prepared) Metadata() map[string]*mcpcontract.Metadata {
	result := map[string]*mcpcontract.Metadata{}
	for _, app := range p.Manifest.Apps {
		if app.prepared != nil {
			result[app.Name] = app.prepared.metadata
		}
	}
	return result
}

// Prepare snapshots local sources, resolves required contracts and generates portable client files.
func Prepare(ctx context.Context, manifest Manifest, options Options) (_ *Prepared, err error) {
	o := options.defaults()
	manifest, err = resolveManifestGraph(ctx, manifest, o)
	if err != nil {
		return nil, err
	}
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
		if p.language != "" {
			p.packages[p.language] = nil
		}
		if isFile(filepath.Join(p.root, "package.json")) {
			p.packages["typescript"] = nil
		}
	}
	results, err := extractTypes(ctx, root, manifest.Apps, o)
	if err != nil {
		return nil, err
	}
	metadata := map[string]*mcpcontract.Metadata{}
	declared := map[string]App{}
	for _, app := range manifest.Apps {
		declared[app.Name] = app
		p := app.prepared
		if p == nil {
			continue
		}
		result, ok := results[app.Name]
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
		if p == nil {
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
				actual, diagnostic, err := resolveRequired(ctx, name, ref, metadata, declared, o)
				if err != nil {
					return nil, fmt.Errorf("%s typed dependency %s: %w", app.Name, name, err)
				}
				if actual == nil || actual.Contract == nil {
					fmt.Fprintf(o.Stderr, "%s: typed dependency %s unavailable: %s\n", app.Name, name, diagnostic)
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
			files, err := generateTypes(ctx, language, request, app.Name, p.metadata.Contract)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", app.Name, err)
			}
			p.packages[language] = files
		}
	}
	if err = currentSources(manifest); err != nil {
		return nil, err
	}
	return &Prepared{Manifest: manifest, root: root, options: o}, nil
}
func generateTypes(ctx context.Context, language string, request mcptype.GenerateRequest, name string, own *mcpcontract.Contract) (map[string]string, error) {
	var requests []mcptype.GenerateRequest
	switch language {
	case "typescript":
		request.ProtocolSpecifier = "../protocol.js"
		node, browser := request, request
		node.Target = "node"
		node.RuntimeSpecifier = "../runtime.mjs"
		browser.Target = "browser"
		browser.BrowserRuntimeSpecifier = "../browser-runtime.mjs"
		if own != nil {
			browser.Contracts = maps.Clone(request.Contracts)
			browser.Contracts[name] = own
		}
		requests = []mcptype.GenerateRequest{node, browser}
	case "python":
		request.Target = "python"
		request.PythonModule = "holon/apps/__init__.py"
		request.PythonRuntimeModule = "holon.client"
		requests = []mcptype.GenerateRequest{request}
	default:
		return nil, fmt.Errorf("unsupported language %s", language)
	}
	files := map[string]string{}
	for _, r := range requests {
		generated, err := mcptype.Generate(ctx, r)
		if err != nil {
			return nil, err
		}
		for name, data := range generated {
			files[name] = string(data)
		}
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
	resolved, err := resolveManifestGraph(ctx, m, o)
	if err != nil {
		return err
	}
	release, err := acquireLocks(resolved)
	if err != nil {
		return err
	}
	defer release()
	for _, app := range resolved.Apps {
		if app.Path != "" {
			if err := ensureDependencies(ctx, app, o); err != nil {
				return err
			}
		}
	}
	prepared, err := Prepare(ctx, m, o)
	if err != nil {
		return err
	}
	defer prepared.Close()
	slots := map[string]string{}
	owners := map[string]string{}
	for _, app := range prepared.Manifest.Apps {
		p := app.prepared
		if p == nil {
			continue
		}
		if _, ok := p.packages["typescript"]; ok {
			slot, err := nodeSlot(app.Path)
			if err != nil {
				return err
			}
			if prior, ok := owners[slot]; ok {
				return fmt.Errorf("apps %s and %s share %s; install a separate %s for each app (avoid hoisting this package)", prior, app.Name, slot, nodePackage)
			}
			owners[slot] = app.Name
			slots[app.Name] = slot
			if err = validateNodeSlot(slot, p.slot); err != nil {
				return err
			}
		}
		if _, ok := p.packages["python"]; ok {
			if err := validatePythonSlot(app.Path); err != nil {
				return err
			}
		}
	}
	if err := currentSources(prepared.Manifest); err != nil {
		return err
	}
	for _, app := range prepared.Manifest.Apps {
		p := app.prepared
		if p == nil {
			continue
		}
		for _, language := range slices.Sorted(maps.Keys(p.packages)) {
			var err error
			if language == "typescript" {
				err = writeNodeSlot(slots[app.Name], p.packages[language], p.slot)
			} else {
				err = writePythonSlot(app.Path, p.packages[language])
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "%s: generated %s types\n", app.Name, language)
		}
	}
	return nil
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
			if slot, err := nodeSlot(app.Path); err == nil {
				paths[slot] = true
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
