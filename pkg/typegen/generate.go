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

func validateEnv(env []string) error {
	for _, entry := range env {
		if !strings.Contains(entry, "=") {
			return fmt.Errorf("environment entry %q must be KEY=VALUE", entry)
		}
	}
	return nil
}

func (o Options) defaults() Options {
	o.HTTPClient = cmp.Or(o.HTTPClient, &http.Client{Timeout: 30 * time.Second})
	o.Stdout = cmp.Or(o.Stdout, io.Discard)
	o.Stderr = cmp.Or(o.Stderr, io.Discard)
	return o
}

type preparedSource struct {
	origin, root, entry, language, fingerprint string
	metadata                                   *mcpcontract.Metadata
	sources                                    map[string]string
}

func (s *preparedSource) contract(ctx context.Context, result extraction) (*mcpcontract.Metadata, error) {
	if result.Diagnostic != "" {
		return nil, fmt.Errorf("%s: %s", s.origin, result.Diagnostic)
	}
	s.sources = result.Sources
	s.metadata.Schema = &mcpcontract.Contract{Tools: result.Tools, Resources: result.Resources}
	if _, err := mcptype.Canonical(ctx, map[string]any{"metadata": s.metadata}); err != nil {
		return nil, fmt.Errorf("%s: %w", s.origin, err)
	}
	return s.metadata, nil
}

type preparedConsumer struct {
	Consumer
	server    *preparedSource
	metadata  *mcpcontract.Metadata
	packages  map[string]map[string]string // files by target
	slot      slotMetadata
	preparing bool
	prepared  bool
}

// Prepared owns temporary source snapshots. Close releases them; preparation never modifies consumers.
type Prepared struct {
	consumers []*preparedConsumer
	paths     map[string]*preparedSource
	root      string
	options   Options
	// resolved holds every non-path source's contract, keyed by alias as each consumer sees it.
	resolved  map[string]*mcpcontract.Metadata
	documents map[string]*Document
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

// Documents returns the own and dependency documents by digest, preserving fetched bytes.
func (p *Prepared) Documents() map[string]*Document { return maps.Clone(p.documents) }

func (p *Prepared) consumer(name string) *preparedConsumer {
	for _, c := range p.consumers {
		if c.Name == name {
			return c
		}
	}
	return nil
}

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
	s.metadata = &mcpcontract.Metadata{Identity: descriptor.Identity()}
	if s.fingerprint, err = snapshotSource(name, origin, s.root); err != nil {
		return nil, err
	}
	return s, ctx.Err()
}

// Prepare snapshots and extracts every path source once, resolves every other source once, and
// generates the client files for each consumer and target. Nothing is written to the consumers.
func Prepare(ctx context.Context, consumers []Consumer, options Options) (_ *Prepared, err error) {
	o := options.defaults()
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
	p := &Prepared{paths: map[string]*preparedSource{}, root: root, options: o, resolved: map[string]*mcpcontract.Metadata{}, documents: map[string]*Document{}}
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
		if _, err = s.contract(ctx, results[name]); err != nil {
			return nil, err
		}
	}
	external := map[string]*Document{}
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

// resolve returns a source's contract document: a snapshot's extraction for a path source, the
// validated document itself for a contract source, or a fetch deduplicated by source key.
func (p *Prepared) resolve(ctx context.Context, source Source, external map[string]*Document) (*Document, error) {
	if source.Path != "" {
		absolute, _ := filepath.Abs(source.Path)
		dependency := p.paths[absolute]
		// When the source is another consumer, reference its complete document.
		for _, consumer := range p.consumers {
			if consumer.Server == nil {
				continue
			}
			own, _ := filepath.Abs(consumer.Server.Path)
			if p.paths[own].origin != dependency.origin {
				continue
			}
			if err := p.prepareConsumer(ctx, consumer, external); err != nil {
				return nil, err
			}
			return NewDocument(ctx, consumer.metadata)
		}
		return NewDocument(ctx, dependency.metadata)
	}
	if source.Contract != nil || source.Document != nil {
		return ResolveDocument(ctx, source, p.options)
	}
	if document, done := external[source.key()]; done {
		return document, nil
	}
	document, err := ResolveDocument(ctx, source, p.options)
	if err == nil && document == nil {
		err = errors.New("image carries no MCP contract document")
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	external[source.key()] = document
	return document, nil
}

func (p *Prepared) prepareConsumer(ctx context.Context, c *preparedConsumer, external map[string]*Document) error {
	if c.prepared {
		return nil
	}
	if c.preparing {
		return fmt.Errorf("contract dependency cycle at %s", c.Name)
	}
	c.preparing = true
	defer func() { c.preparing = false }()
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
	c.metadata = &mcpcontract.Metadata{Identity: identity, Requires: map[string]string{}}
	if c.server != nil {
		c.metadata.Schema = c.server.metadata.Schema
	}
	request := mcptype.GenerateRequest{Identity: identity, Requires: map[string]mcpcontract.Identity{}, Contracts: map[string]*mcpcontract.Contract{}, Banner: mcpcontract.Marker + " app=" + c.Name}
	c.slot = slotMetadata{Marker: mcpcontract.Marker, App: c.Name, Identity: identity}
	aliases := map[string]string{}
	for _, source := range c.Sources {
		metadata, err := p.resolve(ctx, source, external)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		if metadata.Schema == nil {
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
		request.Contracts[alias] = metadata.Schema
		c.metadata.Requires[alias] = metadata.Digest
		p.documents[metadata.Digest] = metadata
		if source.Path == "" {
			p.resolved[alias] = metadata.Metadata
		}
	}
	for _, target := range c.Targets {
		files, err := generateTypes(ctx, target, request, p.options.GoModule)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		c.packages[target] = files
	}
	document, err := NewDocument(ctx, c.metadata)
	if err != nil {
		return err
	}
	p.documents[document.Digest] = document
	c.prepared = true
	return nil
}

func generateTypes(ctx context.Context, target string, request mcptype.GenerateRequest, goModule string) (map[string]string, error) {
	switch target {
	case TargetTS:
		request.Target, request.RuntimeSpecifier, request.ProtocolSpecifier = "node", "../runtime.mjs", "../protocol.js"
	case TargetPy:
		request.Target = "python"
		request.PythonModule = "holon/apps/__init__.py"
		request.PythonRuntimeModule = "holon.client"
	case TargetGo:
		if goModule == "" {
			return nil, errors.New("the go target needs Options.GoModule")
		}
		request.Target = "go"
		request.GoModule = goModule
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
	o := options.defaults()
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
	slots := map[string]string{}
	owners := map[string]string{}
	for _, c := range consumers {
		for _, target := range c.Targets {
			switch target {
			case TargetTS:
				slot, err := ensureNodeRuntime(ctx, c.Dir, nodePackage, o)
				if err != nil {
					return fmt.Errorf("%s: %w", c.Name, err)
				}
				if prior, ok := owners[slot]; ok && prior != c.Name {
					return fmt.Errorf("%s and %s share %s; install a separate %s for each project (avoid hoisting this package)", prior, c.Name, slot, nodePackage)
				}
				owners[slot] = c.Name
				slots[c.Name] = slot
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
			case TargetTS:
				err = validateNodeSlot(slots[c.Name], nodePackage, c.slot)
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
				err = prepared.WriteNodeSlot(c.Name, slots[c.Name])
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
	return writeNodeSlot(pkgDir, nodePackage, files, c.slot)
}

// WritePythonSlot writes the consumer's holon/apps/__init__.py under root, a project directory or
// its staged copy. Only a marked generated module is replaced.
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
		if slot, err := InstalledRuntime(c.Dir, nodePackage); err == nil {
			paths[slot] = true
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
