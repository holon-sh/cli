package typegen

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/tooling"
	"oras.land/oras-go/v2/registry/remote/auth"
)

type Options struct {
	// Env appends KEY=VALUE overrides to the inherited environment.
	Env            []string
	HTTPClient     *http.Client
	Stdout, Stderr io.Writer
	// Credential authenticates OCI registry requests ahead of the Docker credential store.
	Credential auth.CredentialFunc
	// Extract runs one language's extractor: the runner under toolingDir/<language> reads configPath and
	// writes outputPath. It defaults to the host's Node.js or Python interpreter.
	Extract func(ctx context.Context, language, toolingDir, configPath, outputPath string) error
	// GoModule is the runtime module that generated Go clients import; the go target requires it.
	GoModule string
}

func (o Options) defaults() (Options, error) {
	for _, entry := range o.Env {
		if !strings.Contains(entry, "=") {
			return o, fmt.Errorf("environment entry %q must be KEY=VALUE", entry)
		}
	}
	o.HTTPClient = cmp.Or(o.HTTPClient, &http.Client{Timeout: 30 * time.Second})
	o.Stdout = cmp.Or(o.Stdout, io.Discard)
	o.Stderr = cmp.Or(o.Stderr, io.Discard)
	return o, nil
}

// preparedSource is one snapshotted server directory.
type preparedSource struct {
	origin, root, entry, language, fingerprint string
	metadata                                   *mcpcontract.Metadata
	sources                                    map[string]string
}

// contract turns an extraction into the source's contract document; a diagnostic is an error.
func (s *preparedSource) contract(result extraction) (*mcpcontract.Metadata, error) {
	if result.Diagnostic != "" {
		return nil, fmt.Errorf("%s: %s", s.origin, result.Diagnostic)
	}
	s.sources = result.Sources
	s.metadata.Contract = &mcpcontract.Contract{Tools: result.Tools, Resources: result.Resources}
	return s.metadata, nil
}

type preparedConsumer struct {
	Consumer
	server   *preparedSource
	metadata *mcpcontract.Metadata
	packages map[string]map[string]string // files by target
	slot     slotMetadata
}

// Prepared owns temporary source snapshots. Close releases them; preparation never modifies consumers.
type Prepared struct {
	consumers []*preparedConsumer
	paths     map[string]*preparedSource
	root      string
	options   Options
	// resolved holds every non-path source's contract, keyed by alias as each consumer sees it.
	resolved map[string]*mcpcontract.Metadata
}

func (p *Prepared) Close() error { return os.RemoveAll(p.root) }

// Metadata returns each consumer's own contract document (identity, extracted contract and the
// expectations it holds of its sources) by consumer name, plus every resolved source by alias.
func (p *Prepared) Metadata() map[string]*mcpcontract.Metadata {
	result := maps.Clone(p.resolved)
	for _, c := range p.consumers {
		result[c.Name] = c.metadata
	}
	return result
}

func (p *Prepared) consumer(name string) *preparedConsumer {
	for _, c := range p.consumers {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// prepareSource snapshots one server directory under root and reads its identity from server.json.
func prepareSource(ctx context.Context, root, name, path string, o Options) (*preparedSource, error) {
	origin, entry, language, err := serverEntry(path)
	if err != nil {
		return nil, err
	}
	if language == "go" && o.GoModule == "" {
		return nil, fmt.Errorf("%s: Go servers need Options.GoModule", origin)
	}
	s := &preparedSource{origin: origin, root: filepath.Join(root, "sources", name), entry: entry, language: language}
	data, err := os.ReadFile(filepath.Join(origin, "server.json"))
	if err != nil {
		return nil, fmt.Errorf("%s: %w (a server source needs a server.json beside its entry)", origin, err)
	}
	descriptor, err := ParseDescriptor(data)
	if err != nil {
		return nil, fmt.Errorf("%s/server.json: %w", origin, err)
	}
	s.metadata = &mcpcontract.Metadata{Version: mcpcontract.Version, Identity: descriptor.Identity()}
	if s.fingerprint, err = snapshotSource(name, origin, s.root); err != nil {
		return nil, err
	}
	return s, ctx.Err()
}

// Prepare snapshots and extracts every path source once, resolves every other source once, and
// generates the client files for each consumer and target. Nothing is written to the consumers.
func Prepare(ctx context.Context, consumers []Consumer, options Options) (_ *Prepared, err error) {
	o, err := options.defaults()
	if err != nil {
		return nil, err
	}
	for _, c := range consumers {
		if err := c.validate(); err != nil {
			return nil, err
		}
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
	p := &Prepared{paths: map[string]*preparedSource{}, root: root, options: o, resolved: map[string]*mcpcontract.Metadata{}}
	// Every path source, including each consumer's own server, is snapshotted once.
	batches := map[string][]extractionEntry{}
	for _, c := range consumers {
		pc := &preparedConsumer{Consumer: c, packages: map[string]map[string]string{}}
		p.consumers = append(p.consumers, pc)
		sources := c.Sources
		if c.Server != nil {
			sources = append([]Source{*c.Server}, sources...)
		}
		for _, source := range sources {
			if source.Path == "" {
				continue
			}
			absolute, err := filepath.Abs(source.Path)
			if err != nil {
				return nil, err
			}
			if _, done := p.paths[absolute]; done {
				continue
			}
			name := strconv.Itoa(len(p.paths))
			s, err := prepareSource(ctx, root, name, absolute, o)
			if err != nil {
				return nil, err
			}
			p.paths[absolute] = s
			if s.language != "go" {
				batches[s.language] = append(batches[s.language], extractionEntry{Name: name, Path: s.root, Entry: s.entry})
			}
		}
	}
	results, err := extractTypes(ctx, root, batches, o)
	if err != nil {
		return nil, err
	}
	for _, s := range p.paths {
		if s.language == "go" {
			continue
		}
		name := filepath.Base(s.root)
		if _, err = s.contract(results[name]); err != nil {
			return nil, err
		}
	}
	external := map[string]*mcpcontract.Metadata{}
	for _, c := range p.consumers {
		if err = p.prepareConsumer(ctx, c, external); err != nil {
			return nil, err
		}
	}
	if err = p.currentSources(); err != nil {
		return nil, err
	}
	return p, nil
}

// resolve returns a source's contract document, fetching non-path sources once per Prepared.
func (p *Prepared) resolve(ctx context.Context, source Source, external map[string]*mcpcontract.Metadata) (*mcpcontract.Metadata, error) {
	if source.Path != "" {
		absolute, _ := filepath.Abs(source.Path)
		return p.paths[absolute].metadata, nil
	}
	if metadata, done := external[source.key()]; done {
		return metadata, nil
	}
	metadata, err := Resolve(ctx, source, p.options)
	if err == nil && metadata == nil {
		err = errors.New("image carries no MCP contract artifact")
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	external[source.key()] = metadata
	return metadata, nil
}

func (p *Prepared) prepareConsumer(ctx context.Context, c *preparedConsumer, external map[string]*mcpcontract.Metadata) error {
	identity := mcpcontract.Identity{Name: c.Name, Version: "0.0.0"}
	if c.Server != nil {
		absolute, _ := filepath.Abs(c.Server.Path)
		c.server = p.paths[absolute]
		if !pathInsideContext(c.server.origin, c.Dir) {
			return fmt.Errorf("%s: own server %s is outside %s", c.Name, c.server.origin, c.Dir)
		}
		identity = c.server.metadata.Identity
	} else if data, err := os.ReadFile(filepath.Join(c.Dir, "server.json")); err == nil {
		descriptor, err := ParseDescriptor(data)
		if err != nil {
			return fmt.Errorf("%s/server.json: %w", c.Dir, err)
		}
		identity = descriptor.Identity()
	}
	c.metadata = &mcpcontract.Metadata{Version: mcpcontract.Version, Identity: identity, Requires: map[string]mcpcontract.Expectation{}}
	if c.server != nil {
		c.metadata.Contract = c.server.metadata.Contract
	}
	request := mcptype.GenerateRequest{Identity: identity, Requires: map[string]mcpcontract.Identity{}, Contracts: map[string]*mcpcontract.Contract{}, Banner: mcpcontract.Marker + " app=" + c.Name}
	c.slot = slotMetadata{Marker: mcpcontract.Marker, App: c.Name, Identity: identity, Digests: map[string]string{}}
	if c.metadata.Contract != nil {
		canonical, err := mcptype.Canonical(ctx, map[string]any{"contract": c.metadata.Contract})
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		c.slot.Digests[c.Name] = canonical.Digest
	}
	aliases := map[string]string{}
	for _, source := range c.Sources {
		metadata, err := p.resolve(ctx, source, external)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		if metadata.Contract == nil {
			return fmt.Errorf("%s: %s has no contract", c.Name, source)
		}
		alias := cmp.Or(source.Alias, serverKey(metadata.Identity.Name))
		if !appName.MatchString(alias) {
			return fmt.Errorf("%s: cannot derive a client name from %q; write ALIAS=%s", c.Name, metadata.Identity.Name, source)
		}
		if prior, taken := aliases[alias]; taken {
			return fmt.Errorf("%s: %s and %s both become apps.%s; write ALIAS= for one of them", c.Name, prior, source, alias)
		}
		aliases[alias] = source.String()
		request.Requires[alias] = metadata.Identity
		request.Contracts[alias] = metadata.Contract
		c.metadata.Requires[alias] = mcpcontract.Expectation{Identity: metadata.Identity, Contract: *metadata.Contract}
		canonical, err := mcptype.Canonical(ctx, map[string]any{"metadata": metadata})
		if err != nil {
			return err
		}
		c.slot.Digests[alias] = canonical.Digest
		if source.Path == "" {
			p.resolved[alias] = metadata
		}
	}
	own := ""
	if c.server != nil {
		own = cmp.Or(c.Server.Alias, serverKey(identity.Name))
		if prior, taken := aliases[own]; taken && slices.Contains(c.Targets, TargetUI) {
			return fmt.Errorf("%s: own server %s and %s both become %s; write ALIAS= for one of them", c.Name, identity.Name, prior, own)
		}
	}
	for _, target := range c.Targets {
		files, err := generateTypes(ctx, target, request, own, c.metadata.Contract, p.options.GoModule)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		c.packages[target] = files
	}
	return nil
}

func generateTypes(ctx context.Context, target string, request mcptype.GenerateRequest, own string, contract *mcpcontract.Contract, goModule string) (map[string]string, error) {
	switch target {
	case TargetTS:
		request.Target, request.RuntimeSpecifier, request.ProtocolSpecifier = "node", "../runtime.mjs", "../protocol.js"
	case TargetUI:
		// The view calls its own server through the MCP Apps host: that server is the App class.
		request.Target, request.BrowserRuntimeSpecifier, request.ProtocolSpecifier, request.Entry = "browser", "../runtime.mjs", "holon-core/protocol", "index"
		if contract == nil {
			return nil, errors.New("the ui target needs the own server's extracted contract")
		}
		request.Contracts = maps.Clone(request.Contracts)
		request.Contracts[own] = contract
		request.Own = own
	case TargetPy:
		request.Target = "python"
		request.PythonModule = "holon/apps/__init__.py"
		request.PythonRuntimeModule = "holon.client"
	case TargetGo:
		if goModule == "" {
			return nil, errors.New("the go target needs Options.GoModule")
		}
		// The package matches the Holon platform's: unbannered, marked by package.json.
		request.Target = "go"
		request.Banner = ""
		request.GoModule = goModule
		request.PackageFields = map[string]any{"holon": mcpcontract.Marker}
	default:
		return nil, fmt.Errorf("unsupported target %s", target)
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

// currentSources fails when any snapshotted directory changed since preparation.
func (p *Prepared) currentSources() error {
	for _, s := range p.paths {
		fingerprint, err := snapshotSource(filepath.Base(s.root), s.origin, "")
		if err != nil {
			return err
		}
		if fingerprint != s.fingerprint {
			return fmt.Errorf("%s changed during preparation; discarding the superseded update", s.origin)
		}
	}
	return nil
}

// Generate writes each consumer's slots: it requires the slot packages to be installed (installing
// a declared but missing one), prepares, validates the slots, then writes them.
func Generate(ctx context.Context, consumers []Consumer, options Options) error {
	o, err := options.defaults()
	if err != nil {
		return err
	}
	for _, c := range consumers {
		if err := c.validate(); err != nil {
			return err
		}
	}
	release, err := AcquireLocks(consumers)
	if err != nil {
		return err
	}
	defer release()
	type slotKey struct{ target, consumer string }
	slots := map[slotKey]string{}
	owners := map[string]string{}
	for _, c := range consumers {
		for _, target := range c.Targets {
			switch target {
			case TargetTS, TargetUI:
				slot, err := ensureNodeRuntime(ctx, c.Dir, slotPackages[target], o)
				if err != nil {
					return fmt.Errorf("%s: %w", c.Name, err)
				}
				if prior, ok := owners[slot]; ok && prior != c.Name {
					return fmt.Errorf("%s and %s share %s; install a separate %s for each project (avoid hoisting this package)", prior, c.Name, slot, slotPackages[target])
				}
				owners[slot] = c.Name
				slots[slotKey{target, c.Name}] = slot
			case TargetPy:
				if err := ensurePythonRuntime(c.Dir); err != nil {
					return fmt.Errorf("%s: %w", c.Name, err)
				}
			}
		}
	}
	prepared, err := Prepare(ctx, consumers, o)
	if err != nil {
		return err
	}
	defer prepared.Close()
	for _, c := range prepared.consumers {
		for _, target := range c.Targets {
			var err error
			switch target {
			case TargetTS, TargetUI:
				err = validateNodeSlot(slots[slotKey{target, c.Name}], slotPackages[target], c.slot)
			case TargetPy:
				err = validatePythonSlot(c.Dir)
			case TargetGo:
				err = validateGoSlot(c.Dir)
			}
			if err != nil {
				return err
			}
		}
	}
	if err := prepared.currentSources(); err != nil {
		return err
	}
	for _, c := range prepared.consumers {
		for _, target := range c.Targets {
			var err error
			switch target {
			case TargetTS:
				err = prepared.WriteNodeSlot(c.Name, slots[slotKey{target, c.Name}])
			case TargetUI:
				err = prepared.WriteBrowserSlot(c.Name, slots[slotKey{target, c.Name}])
			case TargetPy:
				err = prepared.WritePythonSlot(c.Name, c.Dir)
			case TargetGo:
				err = prepared.WriteGoSlot(c.Name, c.Dir)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "%s: generated %s types\n", c.Name, target)
		}
	}
	return nil
}

// Targets reports the prepared targets of a consumer, in the order given; none for an unknown one.
func (p *Prepared) Targets(consumer string) []string {
	if c := p.consumer(consumer); c != nil {
		return slices.Clone(c.Consumer.Targets)
	}
	return nil
}

// files returns the generated files of one consumer for one target.
func (p *Prepared) files(consumer, target string) (*preparedConsumer, map[string]string, error) {
	c := p.consumer(consumer)
	if c == nil {
		return nil, nil, fmt.Errorf("unknown consumer %q", consumer)
	}
	files, ok := c.packages[target]
	if !ok {
		return nil, nil, fmt.Errorf("%s has no %s clients", consumer, target)
	}
	return c, files, nil
}

// WriteNodeSlot writes the consumer's ts clients and their holon-generated.json marker into
// pkgDir/generated, where pkgDir is an installed holon-sh package or a copy staged for an image.
// It refuses slots that are unmarked, symlinked or owned by another consumer, and leaves an identical slot untouched.
func (p *Prepared) WriteNodeSlot(consumer, pkgDir string) error {
	c, files, err := p.files(consumer, TargetTS)
	if err != nil {
		return err
	}
	return writeNodeSlot(pkgDir, slotPackages[TargetTS], files, c.slot)
}

// WriteBrowserSlot writes the consumer's ui clients into pkgDir/generated, where pkgDir is an
// installed holon-ui package or a copy staged for an image. Same refusals as WriteNodeSlot.
func (p *Prepared) WriteBrowserSlot(consumer, pkgDir string) error {
	c, files, err := p.files(consumer, TargetUI)
	if err != nil {
		return err
	}
	return writeNodeSlot(pkgDir, slotPackages[TargetUI], files, c.slot)
}

// WritePythonSlot writes the consumer's holon/apps/__init__.py under root, a project directory or
// its staged copy. A legacy Holon-managed holon/ package is replaced; user files in the namespace are refused.
func (p *Prepared) WritePythonSlot(consumer, root string) error {
	_, files, err := p.files(consumer, TargetPy)
	if err != nil {
		return err
	}
	return writePythonSlot(root, files)
}

// WriteGoSlot writes the consumer's holon/ Go client package under root. It replaces only an
// absent or Holon-marked holon/ directory.
func (p *Prepared) WriteGoSlot(consumer, root string) error {
	_, files, err := p.files(consumer, TargetGo)
	if err != nil {
		return err
	}
	return writeGoSlot(root, files)
}

// AcquireLocks locks each consumer directory and its installed Node slots until the returned
// release function is called. A failure releases every acquired lock.
func AcquireLocks(consumers []Consumer) (func(), error) {
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
	for _, c := range consumers {
		real, err := filepath.EvalSymlinks(c.Dir)
		if err != nil {
			release()
			return release, err
		}
		paths[real] = true
		for _, pkg := range slotPackages {
			if slot, err := InstalledRuntime(c.Dir, pkg); err == nil {
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
