package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/typegen"
	"k8s.io/utils/ptr"
)

// The public SDKs whose generated/ slots receive TypeScript clients.
const (
	nodeRuntime = "holon-sh"
	uiRuntime   = "holon-ui"
)

// preparedTypes is one path app's share of a typegen preparation: the snapshot its image is built
// from, its contract, and the generated clients typegen writes into the snapshot's or the app's slots.
type preparedTypes struct {
	prepared    *typegen.Prepared
	name        string
	path        string
	root        string
	fingerprint string
	metadata    *mcpcontract.Metadata
	documents   map[string]*typegen.Document
}

type typeWorkspace struct {
	prepared *typegen.Prepared
	accepted map[string]*preparedTypes
	release  func()
}

func (w *typeWorkspace) close() {
	if w.release != nil {
		w.release()
	}
}

func (w *typeWorkspace) accept(manifest manifestSpec) {
	for _, app := range manifest.Apps {
		if p := app.resolved.types; p != nil {
			w.accepted[app.Name] = p
		}
	}
}

// affected reports whether a dependency no longer satisfies the contract app was last built against.
// A contract that cannot be compared counts as affected, so the app is rebuilt.
func (w *typeWorkspace) affected(ctx context.Context, app manifestApp, manifest manifestSpec) bool {
	previous := w.accepted[app.Name]
	return previous != nil && previous.metadata != nil && slices.ContainsFunc(manifest.Apps, func(dependency manifestApp) bool {
		expectation, required := previous.metadata.Requires[dependency.Name]
		p := dependency.resolved.types
		if !required || p == nil {
			return false
		}
		expected := previous.documents[expectation]
		// Accepted preparations have already fetched and verified every required document.
		changes, err := mcptype.Changes(ctx, expected.Metadata, p.metadata)
		return err != nil || len(changes) != 0
	})
}

func (w *typeWorkspace) acquire(manifest manifestSpec) error {
	w.accepted = map[string]*preparedTypes{}
	var consumers []typegen.Consumer
	for _, app := range manifest.Apps {
		if app.Path != "" {
			consumers = append(consumers, typegen.Consumer{Name: app.Name, Dir: app.Path})
		}
	}
	var err error
	w.release, err = typegen.AcquireLocks(consumers)
	return err
}

// prepareManifestTypes snapshots each path app for its image build and prepares its typed contract and
// clients with typegen: extraction (on the host or in a container), dependency contract resolution and
// generation. With a workspace it also writes the clients into the apps for their editors.
func prepareManifestTypes(ctx context.Context, manifest manifestSpec, options buildOptions) (_ manifestSpec, cleanup func(), err error) {
	finish := options.output.measure("apps", "prepare contracts")
	defer finish()
	parent, err := os.MkdirTemp("", "holon-types-")
	if err != nil {
		return manifestSpec{}, func() {}, err
	}
	var prepared *typegen.Prepared
	remove := func() {
		if prepared != nil {
			_ = prepared.Close()
		}
		_ = os.RemoveAll(parent)
	}
	defer func() {
		if err != nil {
			remove()
			cleanup = func() {}
		}
	}()
	// buildx keys BuildKit's incremental context sync by the context directory's base name, so a
	// name derived from the manifest lets each preparation transfer only what changed since the last.
	key := sha256.Sum256([]byte(manifest.file))
	root := filepath.Join(parent, hex.EncodeToString(key[:8]))
	if err = os.Mkdir(root, 0o700); err != nil {
		return manifestSpec{}, nil, err
	}
	manifest.Apps = slices.Clone(manifest.Apps)
	byApp := map[string]typegen.Source{}
	var consumers []typegen.Consumer
	// Only remotes whose schemas a typed caller actually needs are resolved.
	needed := map[string]bool{}
	// Registry credentials stay with the platform, which resolves and publishes image contracts itself.
	typeOptions := typegen.Options{
		Env: options.Env, HTTPClient: options.HTTPClient, Stderr: options.Stderr, Run: options.extractTypes(),
	}
	var pathApps []*manifestApp
	for i := range manifest.Apps {
		app := &manifest.Apps[i]
		switch {
		case app.Path != "":
			p := &preparedTypes{name: app.Name, path: app.Path, root: filepath.Join(root, "sources", app.Name)}
			app.resolved.types = p
			entry, err := typeServer(*app)
			if err != nil {
				return manifestSpec{}, nil, fmt.Errorf("%s: %w", app.Name, err)
			}
			targets, err := typegen.DeclaredTargets(app.Path)
			if err != nil && !errors.Is(err, typegen.ErrNoRuntime) {
				return manifestSpec{}, nil, fmt.Errorf("%s: %w", app.Name, err)
			}
			pathApps = append(pathApps, app)
			byApp[app.Name] = typegen.Source{Path: app.Path}
			if len(targets) != 0 || entry != "" {
				consumer := typegen.Consumer{Name: app.Name, Dir: app.Path, Targets: targets}
				if entry != "" {
					consumer.Server = &typegen.Source{Path: filepath.Join(app.Path, entry)}
				}
				for _, name := range app.resolved.Requires {
					needed[name] = true
					consumer.Sources = append(consumer.Sources, typegen.Source{Alias: name})
				}
				consumers = append(consumers, consumer)
			}
		case app.resolved.Image != "":
			// Pin the deployment image and pass its already resolved contract to typegen.
			image, document, err := resolveTypeMetadata(ctx, app.resolved.Image, options)
			if err != nil {
				return manifestSpec{}, nil, err
			}
			app.resolved.Image = image
			if d := app.resolved.descriptor; d != nil && document != nil && document.Identity != d.Identity() {
				return manifestSpec{}, nil, fmt.Errorf(
					"%s descriptor identity does not match the retained image contract; use the original release identity or a new image tag", app.Name)
			}
			if document != nil {
				byApp[app.Name] = typegen.Source{Document: document}
				for name := range document.Requires {
					needed[name] = true
				}
			}
		}
	}
	for _, app := range manifest.Apps {
		if app.resolved.Remote == nil || !needed[app.Name] {
			continue
		}

		document, err := typegen.ResolveDocument(ctx, typegen.Source{Endpoint: app.resolved.Remote.URL}, typeOptions)
		if err != nil {
			return manifestSpec{}, nil, err
		}
		if d := app.resolved.descriptor; d != nil {
			document.Identity = d.Identity()
			document, err = typegen.NewDocument(ctx, document.Metadata)
			if err != nil {
				return manifestSpec{}, nil, err
			}
		}
		byApp[app.Name] = typegen.Source{Document: document}
	}

	for _, consumer := range consumers {
		for i, dependency := range consumer.Sources {
			source, ok := byApp[dependency.Alias]
			if !ok {
				return manifestSpec{}, nil, fmt.Errorf("%s: dependency %s image carries no MCP contract artifact", consumer.Name, dependency.Alias)
			}
			source.Alias = dependency.Alias
			consumer.Sources[i] = source
		}
	}
	if options.types != nil {
		if err := typegen.Install(ctx, consumers, typeOptions); err != nil {
			return manifestSpec{}, nil, err
		}
	}
	for _, app := range pathApps {
		p := app.resolved.types
		if p.fingerprint, err = snapshotSource(*app, p.root); err != nil {
			return manifestSpec{}, nil, err
		}
	}
	prepared, err = typegen.Prepare(ctx, consumers, typeOptions)
	if err != nil {
		return manifestSpec{}, nil, err
	}
	metadata := prepared.Metadata()
	documents := prepared.Documents()
	for name, source := range byApp {
		if document := source.Document; document != nil {
			metadata[name] = document.Metadata
			documents[document.Digest] = document
		}
	}
	for _, app := range pathApps {
		p := app.resolved.types
		p.prepared = prepared
		p.documents = documents
		p.metadata = metadata[app.Name]
		if d := app.resolved.descriptor; d != nil && p.metadata == nil {
			p.metadata = &mcpcontract.Metadata{Identity: d.Identity()}
		}
		metadata[app.Name] = p.metadata
	}
	if err := currentTypeSources(manifest); err != nil {
		return manifestSpec{}, nil, err
	}
	if w := options.types; w != nil {
		if err := prepared.Write(); err != nil {
			return manifestSpec{}, nil, err
		}
		w.prepared = prepared
	}

	for _, app := range manifest.Apps {
		requires := ptr.Deref(metadata[app.Name], mcpcontract.Metadata{}).Requires
		for _, name := range slices.Sorted(maps.Keys(requires)) {
			expected, err := fetchTypeDocument(ctx, app.resolved.Image, requires[name], documents, options)
			if err != nil {
				return manifestSpec{}, nil, fmt.Errorf("%s: fetch expected %s contract %s: %w", app.Name, name, requires[name], err)
			}
			changes, err := mcptype.Changes(ctx, expected, metadata[name])
			if err != nil {
				return manifestSpec{}, nil, err
			}
			if len(changes) != 0 {
				texts := make([]string, len(changes))
				for i, change := range changes {
					texts[i] = change.Text
				}
				return manifestSpec{}, nil, fmt.Errorf(
					"%s has changed the typed contract expected by %s (%s); include both in holon deploy %s %s (or rebuild and publish the caller)",
					name, app.Name, strings.Join(texts, ", "), name, app.Name)
			}
		}
	}
	return manifest, remove, nil
}

// typeServer is the path app's own MCP server entry, or "" for an app that is not an MCP server: one
// without a server entry, or JavaScript that never mentions an MCP SDK or holon-sh.
func typeServer(app manifestApp) (string, error) {
	root := app.Path
	entry, err := typegen.SourceEntry(root)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(entry, ".ts") || strings.HasSuffix(entry, ".js") {
		if !mentionsMCP(root) {
			return "", nil
		}
	}
	return entry, nil
}

func currentTypeSources(manifest manifestSpec) error {
	for _, app := range manifest.Apps {
		p := app.resolved.types
		if p == nil {
			continue
		}
		fingerprint, err := snapshotSource(app, "")
		if err != nil {
			return err
		}
		if fingerprint != p.fingerprint {
			return errors.New("application source changed during preparation; discarding the superseded update")
		}
	}
	return nil
}
