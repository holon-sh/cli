package typegen

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/tooling"
	"github.com/opencontainers/go-digest"
)

// unversioned stands in when no source names a version.
const unversioned = "0.0.0"

type SignIn func(ctx context.Context, origin string) (string, error)

type Options struct {
	Env            []string
	HTTPClient     *http.Client
	Stdout, Stderr io.Writer
	SignIn         SignIn
	// Run runs one server through its language's contract runner, toolingDir/<language>/runner.mjs or
	// extract.py, with args (contract ROOT ENTRY), copying the runner's stdout to stdout. A failure the
	// runner reports is a *RunnerError. It defaults to the host's Node.js or Python interpreter, which
	// runs the server in its own directory and stops it after 30 seconds.
	Run    func(ctx context.Context, language, toolingDir string, args []string, stdout io.Writer) error
	Served map[string]json.RawMessage
}

func (o Options) defaults() Options {
	o.HTTPClient = cmp.Or(o.HTTPClient, &http.Client{Timeout: 30 * time.Second})
	o.Stdout = cmp.Or(o.Stdout, io.Discard)
	o.Stderr = cmp.Or(o.Stderr, io.Discard)
	return o
}

type preparedSource struct {
	origin, name, entry, language, fingerprint string
	metadata                                   *mcpcontract.Metadata
	server                                     *mcpregistry.Descriptor
}

func (s *preparedSource) extract(result *extraction) error {
	contract, err := listCatalog(result.Capabilities, result.pages)
	if err != nil {
		return fmt.Errorf("%s: %w", s.origin, err)
	}
	s.metadata.Schema = contract
	if s.metadata.Identity == (mcpcontract.Identity{}) {
		descriptor, err := parseDescriptor(result.ServerInfo)
		if err != nil {
			return fmt.Errorf("%s: %w; declare the server's name, version and description in its MCP constructor, or add a server.json", s.origin, err)
		}
		s.metadata.Identity, s.server = descriptor.Identity(), descriptor
	}
	return nil
}

type preparedConsumer struct {
	Consumer
	server   *preparedSource
	metadata *mcpcontract.Metadata
	packages map[string]map[string][]byte // files by target
	document *Document
}

// Prepared owns a temporary directory for tooling and documents. Close releases it; preparation
// never modifies consumers.
type Prepared struct {
	consumers []*preparedConsumer
	paths     map[string]*preparedSource
	root      string
	options   Options
	// resolved holds every non-path source's contract, keyed by alias as each consumer sees it.
	resolved  map[string]*mcpcontract.Metadata
	documents map[string]*Document
	// fetched holds each registry and endpoint source's resolution, keyed by source.
	fetched map[string]*fetch
}

type fetch struct {
	document *Document
	err      error
}

// fetchSources resolves each distinct registry and endpoint source concurrently. The returned
// function waits for every resolution and returns them by source key.
func fetchSources(ctx context.Context, consumers []Consumer, o Options) func() map[string]*fetch {
	fetched := map[string]*fetch{}
	var group sync.WaitGroup
	for _, c := range consumers {
		for _, source := range c.Sources {
			if _, started := fetched[source.key()]; started || source.Path != "" || source.Document != nil {
				continue
			}
			result := &fetch{}
			fetched[source.key()] = result
			group.Go(func() { result.document, result.err = ResolveDocument(ctx, source, o) })
		}
	}
	return func() map[string]*fetch {
		group.Wait()
		return fetched
	}
}

func (p *Prepared) Close() error { return os.RemoveAll(p.root) }

// Tooling is the directory the contract runners and checkers are unpacked in, until Close.
func (p *Prepared) Tooling() string { return filepath.Join(p.root, "tooling") }

// Metadata returns each consumer's own contract document (identity, extracted contract and the
// expectations it holds of its sources) by consumer name, plus every resolved source by alias.
func (p *Prepared) Metadata() map[string]*mcpcontract.Metadata {
	result := maps.Clone(p.resolved)
	for _, c := range p.consumers {
		result[c.Name] = c.metadata
	}
	return result
}

// Servers returns how each consumer's own server describes itself, by consumer name.
func (p *Prepared) Servers() map[string]*mcpregistry.Descriptor {
	result := map[string]*mcpregistry.Descriptor{}
	for _, c := range p.consumers {
		if c.server != nil {
			result[c.Name] = c.server.server
		}
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

func prepareSource(ctx context.Context, name, path string) (*preparedSource, error) {
	origin, entry, language, err := serverEntry(path)
	if err != nil {
		return nil, err
	}
	s := &preparedSource{origin: origin, name: name, entry: entry, language: language, metadata: &mcpcontract.Metadata{}}
	descriptor, err := readDescriptor(origin)
	switch {
	case err != nil:
		return nil, err
	case descriptor != nil:
		s.metadata.Identity, s.server = descriptor.Identity(), descriptor
	case language == "go":
		return nil, fmt.Errorf("%s: a Go server source needs a server.json beside its entry", origin)
	}
	// The runners import the server where it lives, beside its installed dependencies, so the
	// fingerprint guards against changes until preparation finishes.
	if s.fingerprint, err = SnapshotSource(name, origin, ""); err != nil {
		return nil, err
	}
	return s, ctx.Err()
}

// Prepare fingerprints and extracts every path source once, resolves every other source once, and
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
	p := &Prepared{paths: map[string]*preparedSource{}, root: root, options: o, resolved: map[string]*mcpcontract.Metadata{}, documents: map[string]*Document{}}
	// Registry and endpoint sources are fetched while path sources are extracted, and stopped when
	// preparation fails.
	ctx, cancel := context.WithCancel(ctx)
	fetched := fetchSources(ctx, consumers, o)
	defer fetched()
	defer cancel()
	var paths []*preparedSource
	for _, c := range consumers {
		pc := &preparedConsumer{Consumer: c, packages: map[string]map[string][]byte{}}
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
			s, err := prepareSource(ctx, name, absolute)
			if err != nil {
				return nil, err
			}
			p.paths[absolute] = s
			paths = append(paths, s)
		}
	}
	if len(paths) != 0 {
		if err = tooling.Unpack(filepath.Join(root, "tooling")); err != nil {
			return nil, err
		}
	}
	if err = extractSources(ctx, root, paths, o, true); err != nil {
		return nil, err
	}
	p.fetched = fetched()
	for _, c := range p.consumers {
		if err = p.prepareConsumer(ctx, c); err != nil {
			return nil, err
		}
	}
	if err = p.currentSources(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Prepared) resolve(ctx context.Context, source Source) (*Document, error) {
	if source.Path != "" {
		absolute, _ := filepath.Abs(source.Path)
		dependency := p.paths[absolute]
		for _, consumer := range p.consumers {
			if consumer.Server == nil {
				continue
			}
			own, _ := filepath.Abs(consumer.Server.Path)
			if p.paths[own].origin != dependency.origin {
				continue
			}
			if err := p.prepareConsumer(ctx, consumer); err != nil {
				return nil, err
			}
			return consumer.document, nil
		}
		return NewDocument(dependency.metadata)
	}
	if source.Document != nil {
		return ParseDocument(source.Document.Data)
	}
	fetched := p.fetched[source.key()]
	if fetched.err != nil {
		return nil, fmt.Errorf("%s: %w", source, fetched.err)
	}
	return fetched.document, nil
}

func (p *Prepared) prepareConsumer(ctx context.Context, c *preparedConsumer) error {
	if c.document != nil {
		return nil
	}
	// Metadata exists while visiting dependencies; a document marks completion.
	if c.metadata != nil {
		return fmt.Errorf("contract dependency cycle at %s", c.Name)
	}
	identity := mcpcontract.Identity{Name: c.Name, Version: unversioned}
	var alias string
	if c.Server != nil {
		alias = c.Server.Alias
		absolute, _ := filepath.Abs(c.Server.Path)
		c.server = p.paths[absolute]
		if !lexicallyInside(c.server.origin, c.Dir) {
			return fmt.Errorf("%s: own server %s is outside %s", c.Name, c.server.origin, c.Dir)
		}
		identity = c.server.metadata.Identity
	} else if descriptor, err := readDescriptor(c.Dir); err != nil {
		return err
	} else if descriptor != nil {
		identity = descriptor.Identity()
	} else if manifest, err := manifestIdentity(c.Dir, c.Targets); err != nil {
		return err
	} else if manifest.Name != "" {
		identity = manifest
	}
	c.metadata = &mcpcontract.Metadata{Identity: identity, Requires: map[string]string{}}
	if c.server != nil {
		c.metadata.Schema = c.server.metadata.Schema
	}
	request := mcptype.GenerateRequest{Identity: identity, Requires: map[string]mcpcontract.Identity{}, Contracts: map[string]*mcpcontract.Contract{}, Banner: mcpcontract.Marker + " app=" + c.Name}
	aliases := map[string]string{}
	for _, source := range c.Sources {
		metadata, err := p.resolve(ctx, source)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		if metadata.Schema == nil {
			return fmt.Errorf("%s: %s has no contract", c.Name, source)
		}
		alias := cmp.Or(source.Alias, ServerKey(metadata.Identity.Name))
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
		files, err := generateTypes(ctx, target, request, c.metadata, alias, c.Name)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		c.packages[target] = files
	}
	document, err := NewDocument(c.metadata)
	if err != nil {
		return err
	}
	p.documents[document.Digest] = document
	c.document = document
	return nil
}

// generateTypes generates one target's clients. Browser clients call the own server under its alias
// or derived name, at the endpoint Holon serves app at; an alias also makes it a client in every
// other slot.
func generateTypes(ctx context.Context, target string, request mcptype.GenerateRequest, own *mcpcontract.Metadata, alias, app string) (map[string][]byte, error) {
	switch target {
	case TargetTS:
		request.Target = "node"
	case TargetUI:
		request.Target = "browser"
		alias = cmp.Or(alias, ServerKey(own.Identity.Name), "app")
	case TargetPy:
		request.Target = "python"
	case TargetGo:
		request.Target, request.GoModule = "go", GoRuntimeModule
	}
	if own.Schema != nil && alias != "" {
		if _, exists := request.Contracts[alias]; exists {
			return nil, fmt.Errorf("own client %s conflicts with a dependency alias", alias)
		}
		request.Own, request.Endpoint = alias, "/mcp/"+app
		request.Requires, request.Contracts = maps.Clone(request.Requires), maps.Clone(request.Contracts)
		request.Requires[alias], request.Contracts[alias] = own.Identity, own.Schema
	}
	return mcptype.Generate(ctx, request)
}

// currentSources fails when any source directory changed since preparation.
func (p *Prepared) currentSources() error {
	for _, s := range p.paths {
		fingerprint, err := SnapshotSource(s.name, s.origin, "")
		if err != nil {
			return err
		}
		if fingerprint != s.fingerprint {
			return fmt.Errorf("%s changed during preparation; discarding the superseded update", s.origin)
		}
	}
	return nil
}

// Generate maps #holon-sh and #holon-ui to the clients in package.json, installs missing declared
// SDK runtimes with the project's package manager, prepares clients, and writes the project slots.
func Generate(ctx context.Context, consumers []Consumer, options Options) error {
	release, err := AcquireLocks(consumers)
	if err != nil {
		return err
	}
	defer release()
	if err := MapClients(consumers, options); err != nil {
		return err
	}
	o := options.defaults()
	for _, c := range consumers {
		for _, target := range c.Targets {
			switch target {
			case TargetTS, TargetUI:
				err = ensureNodeRuntime(ctx, c.Dir, NodePackages[target], o)
			case TargetPy:
				err = ensurePythonRuntime(ctx, c.Dir, o)
			}
			if err != nil {
				return fmt.Errorf("%s: %w", c.Name, err)
			}
		}
	}
	prepared, err := Prepare(ctx, consumers, options)
	if err != nil {
		return err
	}
	defer prepared.Close()
	return prepared.Write()
}

// Write validates all consumer slots before writing any of them. Callers hold AcquireLocks and
// install dependencies before Prepare.
func (p *Prepared) Write() error {
	for _, c := range p.consumers {
		for _, target := range c.Targets {
			if err := validateSlot(c.Dir, target); err != nil {
				return fmt.Errorf("%s: %w", c.Name, err)
			}
		}
	}
	for _, c := range p.consumers {
		if err := c.write(c.Dir); err != nil {
			return err
		}
		for _, target := range c.Targets {
			fmt.Fprintf(p.options.Stdout, "%s: generated %s types\n", c.Name, target)
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

// WriteClients writes a consumer's clients into dir, the project or a copy staged for an image, and
// nothing for an unknown one. It refuses to overwrite files it did not generate, and leaves identical
// clients untouched.
func (p *Prepared) WriteClients(consumer, dir string) error {
	if c := p.consumer(consumer); c != nil {
		return c.write(dir)
	}
	return nil
}

func (c *preparedConsumer) write(dir string) error {
	for _, target := range c.Targets {
		if err := writeSlot(dir, target, c.packages[target]); err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
	}
	return nil
}

// AcquireLocks locks each consumer directory until the returned release function is called. A
// failure releases every acquired lock.
func AcquireLocks(consumers []Consumer) (func(), error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(cache, "holon", "type-locks")
	if err = os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for _, c := range consumers {
		real, err := filepath.EvalSymlinks(c.Dir)
		if err != nil {
			return nil, err
		}
		paths[real] = true
	}
	var locks []*flock.Flock
	release := func() {
		for _, lock := range locks {
			_ = lock.Unlock()
		}
	}
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		lock := flock.New(filepath.Join(directory, digest.FromString(path).Encoded()+".lock"))
		ok, err := lock.TryLock()
		if err == nil && !ok {
			err = fmt.Errorf("%s is already managed by another holon command; stop it before changing dependency bindings", path)
		}
		if err != nil {
			release()
			return nil, err
		}
		locks = append(locks, lock)
	}
	return release, nil
}
