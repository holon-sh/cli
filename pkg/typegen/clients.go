package typegen

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/holon-sh/cli/pkg/tooling"
)

type clientUsage struct {
	UsesApps   bool     `json:"usesApps"`
	References []string `json:"references"`
	Imports    []string `json:"imports"`
}

func inspectClients(ctx context.Context, m Manifest, o Options) (map[string]clientUsage, error) {
	files := map[string]map[string][]string{}
	for _, app := range m.Apps {
		if app.Path == "" || !m.pathRequiresOnly && len(o.Apps) > 0 && !o.selects(app.Name) {
			continue
		}
		err := filepath.WalkDir(app.Path, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if entry.IsDir() {
				if path != app.Path && (strings.HasPrefix(entry.Name(), ".holon") || generatedSource(app.Path, path)) {
					return filepath.SkipDir
				}
			} else if entry.Type().IsRegular() {
				for language, tool := range typeLanguages {
					if slices.Contains(tool.extensions, filepath.Ext(path)) {
						if files[language] == nil {
							files[language] = map[string][]string{}
						}
						files[language][app.Name] = append(files[language][app.Name], path)
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	usage := map[string]clientUsage{}
	if len(files) == 0 {
		return usage, nil
	}
	root, err := os.MkdirTemp("", "holon-clients-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	if err := tooling.Unpack(filepath.Join(root, "tooling")); err != nil {
		return nil, err
	}
	for language, apps := range files {
		var found map[string]clientUsage
		if err := runTypeTool(ctx, root, language, "clients", apps, &found, o); err != nil {
			return nil, err
		}
		for name, result := range found {
			prior := usage[name]
			usage[name] = clientUsage{UsesApps: prior.UsesApps || result.UsesApps, References: append(prior.References, result.References...), Imports: append(prior.Imports, result.Imports...)}
		}
	}
	return usage, nil
}
