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
	"k8s.io/utils/ptr"
)

// preparedTypes is one path app's share of a typegen preparation: the snapshot its image is built
// from, its contract, and the generated clients typegen writes into the snapshot or the app.
type preparedTypes struct {
	prepared    *typegen.Prepared
	name        string
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
	needed := map[string]bool{}
	typeOptions := typegen.Options{Env: options.Env, HTTPClient: options.HTTPClient, Stderr: options.output.logs()}
	prepareOptions := typeOptions
	if options.build != nil {
		prepareOptions.Run = options.build.extract
	}
	var pathApps []*manifestApp
	for i := range manifest.Apps {
		app := &manifest.Apps[i]
		switch {
		case app.Path != "":
			if clientutil.PathInside(parent, app.Path) {
				return manifestSpec{}, nil, fmt.Errorf("%s: build metadata must be outside application source", app.Name)
			}
			answer, ok := served[app.Name]
			if ok {
				if err := checkAccess(answer, app.resolved.Access); err != nil {
					return manifestSpec{}, nil, fmt.Errorf("%s: %w", app.Name, err)
				}
			}
			p := &preparedTypes{name: app.Name, root: filepath.Join(parent, "sources", app.Name), unserved: served != nil && !ok}
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
					needed[name] = true
				}
				consumers = append(consumers, *consumer)
			}
		case app.resolved.Image != "":
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
				byApp[app.Name] = document
				for name := range document.Requires {
					needed[name] = true
				}
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
	session := cmp.Or(options.session, protocol.ProductionName)
	declared := session
	var remotes []protocol.App
	for _, app := range manifest.Apps {
		if app.resolved.Remote != nil && needed[app.Name] {
			remotes = append(remotes, app.resolved.App)
		}
	}
	if options.build != nil {
		if err := options.build.start(ctx, pathApps, remotes); err != nil {
			return manifestSpec{}, nil, err
		}
		declared = cmp.Or(options.build.session.Name, declared)
	}
	unread := map[string]error{}
	for _, app := range remotes {
		if declared == protocol.ProductionName && len(protocol.ChangedApps([]protocol.App{manifest.deployed[app.Name]}, []protocol.App{app})) != 0 {
			unread[app.Name] = errors.New("production does not run this declaration yet")
			continue
		}
		answer, err := options.manager.appContract(ctx, declared, app.Name)
		var document *typegen.Document
		if err == nil {
			document, err = answer.Advertised()
		}
		if err != nil {
			unread[app.Name] = err
			continue
		}
		byApp[app.Name] = document
	}
	installed := manifest.installed()
	for _, name := range installed {
		if _, deployed := manifest.deployed[name]; !needed[name] && deployed {
			continue
		}
		answer, err := options.manager.appContract(ctx, session, name)
		var document *typegen.Document
		if err == nil {
			document, err = answer.Document(name)
		}
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
			server, ok := byApp[name]
			if ok {
				consumers[i].Sources = append(consumers[i].Sources, typegen.Source{Alias: name, Server: server})
			} else if unread[name] == nil {
				return manifestSpec{}, nil, fmt.Errorf("%s: dependency %s carries no MCP contract to type calls against", consumers[i].Name, name)
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
			documents[document.Digest] = document
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
		requires := ptr.Deref(metadata[app.Name], mcpcontract.Metadata{}).Requires
		for _, name := range slices.Sorted(maps.Keys(requires)) {
			if unread[name] != nil {
				continue
			}
			expected, err := fetchTypeDocument(ctx, app.resolved.Image, requires[name], documents, options)
			if err != nil {
				return manifestSpec{}, nil, fmt.Errorf("%s: fetch expected %s contract %s: %w", app.Name, name, requires[name], err)
			}
			changes, err := mcptype.Changes(expected, metadata[name])
			if err != nil {
				return manifestSpec{}, nil, err
			}
			if len(changes) == 0 {
				continue
			}
			advice := fmt.Sprintf("include both in holon deploy %s %s (or rebuild and publish the caller)", name, app.Name)
			if slices.Contains(installed, name) {
				advice = "rebuild and publish the caller"
			}
			return manifestSpec{}, nil, fmt.Errorf("%s has changed the typed contract expected by %s (%s); %s", name, app.Name, strings.Join(changes, ", "), advice)
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
