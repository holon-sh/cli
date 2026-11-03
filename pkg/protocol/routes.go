package protocol

import (
	"cmp"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"
)

var platformPaths = strings.Fields(PlatformPaths)

func validMount(raw string) bool {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Path != raw || u.RawPath != "" || u.EscapedPath() != u.Path || u.Path == MCPPath ||
		!strings.HasPrefix(u.Path, "/") || strings.ContainsAny(u.Path, "\\\x00") || path.Clean(u.Path) != u.Path {
		return false
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
	return !slices.Contains(platformPaths, "/"+first)
}

func AppBrowserMount(app App, mode string) string {
	mount := "/" + app.Name
	if mode == ModeProduction {
		return cmp.Or(app.Mount, mount)
	}
	return mount
}

func ValidateAppMounts(apps []App) error {
	seen := map[string]bool{}
	for _, app := range apps {
		if app.Remote != nil {
			if app.Mount != "" {
				return fmt.Errorf("app %s browser routing requires a local app", app.Name)
			}
			continue
		}
		mount := AppBrowserMount(app, ModeProduction)
		if !validMount(mount) {
			return fmt.Errorf("app %s mount requires a canonical, non-platform browser path", app.Name)
		}
		if seen[mount] {
			return fmt.Errorf("app %s mount must be unique", app.Name)
		}
		seen[mount] = true
	}
	return nil
}
