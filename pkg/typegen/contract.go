package typegen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/tooling"
)

// Contract retrieves one provider's contract without resolving its callers or generating clients.
func Contract(ctx context.Context, manifest Manifest, name string, options Options) (*mcpcontract.Metadata, error) {
	o := options.defaults()
	for _, app := range manifest.Apps {
		if app.Name != name {
			continue
		}
		if app.Path == "" {
			if app.Registry != nil {
				descriptor, err := (Client{HTTP: o.HTTPClient, Origin: app.Registry.RegistryURL()}).Get(ctx, app.Registry.Name, app.Registry.Version)
				if err != nil {
					return nil, err
				}
				return resolveDescriptor(ctx, descriptor, o)
			}
			if app.Image != "" {
				return resolveTypeMetadata(ctx, app.Image, o)
			}
			return nil, errors.New("a remote app without a registry identity requires --url, --name and --version")
		}
		root, err := os.MkdirTemp("", "holon-contract-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(root)
		if err = tooling.Unpack(filepath.Join(root, "tooling")); err != nil {
			return nil, err
		}
		p := &preparedTypes{root: filepath.Join(root, "sources", app.Name)}
		app.prepared = p
		if _, err = snapshotSource(app, p.root); err != nil {
			return nil, err
		}
		if p.entry, p.language, err = analysisEntry(p.root); err != nil {
			return nil, err
		}
		identity := mcpcontract.Identity{Name: app.Name, Version: "0.0.0"}
		data, err := os.ReadFile(filepath.Join(p.root, "server.json"))
		if err == nil {
			descriptor, err := ParseDescriptor(data)
			if err != nil {
				return nil, err
			}
			identity = descriptor.Identity()
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		results, err := extractTypes(ctx, root, []App{app}, o)
		if err != nil {
			return nil, err
		}
		result, ok := results[app.Name]
		if !ok {
			return nil, fmt.Errorf("%s has no statically extracted contract", app.Name)
		}
		if result.Diagnostic != "" {
			return nil, fmt.Errorf("%s: %s", app.Name, result.Diagnostic)
		}
		return &mcpcontract.Metadata{Version: mcpcontract.Version, Identity: identity, Contract: &mcpcontract.Contract{Tools: result.Tools, Resources: result.Resources}}, nil
	}
	return nil, fmt.Errorf("unknown app %q", name)
}
