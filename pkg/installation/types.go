package installation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcptype"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/holon-sh/protocol"
)

// preparedTypes is one path app's share of a typegen preparation: the snapshot its image is built
// from, its contract, and the generated clients typegen writes into the snapshot or the app.
type preparedTypes struct {
	prepared    *typegen.Prepared
	root        string
	fingerprint string
	metadata    *mcpcontract.Metadata
	documents   map[string]*typegen.Document
	unserved    bool
}

func lockSources(manifest manifestSpec) (func(), error) {
	var consumers []typegen.Consumer
	for _, app := range manifest.Apps {
		if app.Path != "" {
			consumers = append(consumers, typegen.Consumer{Name: app.Name, Dir: app.Path})
		}
	}
	return typegen.AcquireLocks(consumers)
}

func prepareManifestTypes(
	ctx context.Context, manifest manifestSpec, options buildOptions, served map[string]*typegen.Served,
) (_ manifestSpec, _ func(), err error) {
	finish := options.output.measure("apps", "prepare contracts")
	defer finish()
	parent, err := os.MkdirTemp("", "holon-types-")
	if err != nil {
		return manifestSpec{}, nil, err
	}
	remove := func() { _ = os.RemoveAll(parent) }
	defer func() {
		if err != nil {
			remove()
		}
	}()
	manifest.Apps = slices.Clone(manifest.Apps)
	byApp := map[string]typegen.Server{}
	var consumers []typegen.Consumer
	typed := map[string]bool{}
	typeOptions := typegen.Options{Env: options.Env, HTTPClient: options.HTTPClient, Stderr: options.output.logs()}
	prepareOptions := typeOptions
	if options.build != nil {
		prepareOptions.Run = options.build.extract
	}
	var pathApps []*manifestApp
	for i := range manifest.Apps {
		app := &manifest.Apps[i]
		if app.Path != "" {
			if clientutil.PathInside(parent, app.Path) {
				return manifestSpec{}, nil, fmt.Errorf("%s: build metadata must be outside application source", app.Name)
			}
			answer, ok := served[app.Name]
			if ok {
				if err := checkAccess(answer, app.resolved.Access); err != nil {
					return manifestSpec{}, nil, fmt.Errorf("%s: %w", app.Name, err)
				}
			}
			p := &preparedTypes{root: filepath.Join(parent, "sources", app.Name), unserved: served != nil && !ok}
			app.resolved.types = p
			consumer, err := typegen.AppConsumer(app.Name, app.Path)
			if err == nil && consumer != nil && !slices.Contains([]string{"", app.Path}, consumer.Server.Dir) {
				err = fmt.Errorf("holon dev and holon deploy run the server entry at an app's root, and this app keeps its MCP server in %s; ship it as an image: app", consumer.Server.Dir)
			}
			if err != nil {
				return manifestSpec{}, nil, fmt.Errorf("%s: %w", app.Name, err)
			}
			pathApps = append(pathApps, app)
			byApp[app.Name] = typegen.Path{Dir: app.Path, Served: answer}
			if consumer != nil {
				if p.unserved {
					consumer.Server = typegen.Path{}
				} else if consumer.Server.Dir == app.Path {
					consumer.Server.Served = answer
				}
				for _, name := range app.resolved.Requires {
					typed[name] = true
				}
				consumers = append(consumers, *consumer)
			}
		}
	}
	if options.host == protocol.DevHost {
		if err := typegen.MapClients(consumers, typeOptions); err != nil {
			return manifestSpec{}, nil, err
		}
	}
	for _, app := range pathApps {
		p := app.resolved.types
		if p.fingerprint, err = snapshotSource(*app, p.root); err != nil {
			return manifestSpec{}, nil, err
		}
	}
	if options.host == protocol.DevHost && served == nil {
		return manifest, remove, nil
	}
	var started []protocol.App
	needed := maps.Clone(typed)
	for _, app := range manifest.Apps {
		if app.Path == "" && typed[app.Name] {
			started = append(started, app.resolved.App)
		}
		for name := range app.resolved.expects {
			needed[name] = true
		}
	}
	running := options.session
	if options.build != nil {
		if err := options.build.start(ctx, pathApps, started); err != nil {
			return manifestSpec{}, nil, err
		}
		running = options.build.session.Name
	}
	unread := map[string]error{}
	for _, app := range started {
		document, err := options.manager.appDocument(ctx, running, app.Name)
		if err != nil {
			unread[app.Name] = err
			continue
		}
		byApp[app.Name] = document
	}
	for _, name := range manifest.installed() {
		if _, deployed := manifest.deployed[name]; !needed[name] && deployed {
			continue
		}
		document, err := options.manager.appDocument(ctx, cmp.Or(options.session, protocol.ProductionName), name)
		if HTTPStatus(err) == http.StatusNotFound {
			return manifestSpec{}, nil, fmt.Errorf("%s is neither an app in this project nor on the installation; declare it in holon.yaml or add it to the installation", name)
		}
		if err != nil {
			unread[name] = err
			continue
		}
		byApp[name] = document
	}
	var reported []error
	for _, name := range slices.Sorted(maps.Keys(unread)) {
		reported = append(reported, fmt.Errorf("%s: %w", name, unread[name]))
	}
	options.output.problem("MCP contracts", errors.Join(reported...))
	for i := range consumers {
		app, _ := manifest.app(consumers[i].Name)
		for _, name := range app.resolved.Requires {
			if server, ok := byApp[name]; ok {
				consumers[i].Sources = append(consumers[i].Sources, typegen.Source{Alias: name, Server: server})
			}
		}
	}
	prepared, err := typegen.Prepare(ctx, consumers, prepareOptions)
	if err != nil {
		return manifestSpec{}, nil, err
	}
	metadata := prepared.Metadata()
	documents := prepared.Documents()
	servers := prepared.Servers()
	for name, server := range byApp {
		if document, ok := server.(*typegen.Document); ok {
			metadata[name] = document.Metadata
		}
	}
	for _, app := range pathApps {
		p := app.resolved.types
		p.prepared = prepared
		p.documents = documents
		p.metadata = metadata[app.Name]
		app.resolved.descriptor = cmp.Or(app.resolved.descriptor, servers[app.Name])
		if d := app.resolved.descriptor; d != nil && p.metadata == nil {
			p.metadata = &mcpcontract.Metadata{Identity: d.Identity()}
		}
		metadata[app.Name] = p.metadata
	}
	if options.host == protocol.DevHost {
		if err := prepared.Write(io.Discard); err != nil {
			return manifestSpec{}, nil, err
		}
	}
	for _, app := range manifest.Apps {
		for _, name := range slices.Sorted(maps.Keys(app.resolved.expects)) {
			if dependency, err := manifest.app(name); unread[name] != nil || err == nil && dependency.Path == "" {
				continue
			}
			changes, err := mcptype.Changes(app.resolved.expects[name], metadata[name])
			if err != nil {
				return manifestSpec{}, nil, err
			}
			if len(changes) != 0 {
				return manifestSpec{}, nil, fmt.Errorf("%s has changed the typed contract expected by %s (%s); rebuild and publish the caller", name, app.Name, strings.Join(changes, ", "))
			}
		}
	}
	return manifest, remove, nil
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

func checkAccess(served *typegen.Served, access *protocol.AppAccess) error {
	findings, err := served.Check(access.Tools)
	if err != nil {
		return err
	}
	for _, finding := range findings {
		if finding.Rule == typegen.RuleUnlistedTool {
			return fmt.Errorf("access.tools[%s] names a tool the app does not list", finding.Tool)
		}
	}
	return nil
}
