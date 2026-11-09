package scaffold

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"

	"github.com/holon-sh/cli/internal/clientutil"
	"github.com/holon-sh/cli/pkg/typegen"
)

// registries are where Resolve looks up SDK releases.
type registries struct{ npm, pypi string }

var client = &http.Client{Timeout: 30 * time.Second}

// Resolve picks the SDK releases a new app in language pins, from npm's registry, or the one
// npm_config_registry names, and PyPI: on npm, the newest release that reads the clients this holon
// writes (holon.contractVersion); on PyPI, the newest release.
func Resolve(ctx context.Context, language string) (Versions, error) {
	npm := cmp.Or(os.Getenv("npm_config_registry"), os.Getenv("NPM_CONFIG_REGISTRY"), "https://registry.npmjs.org")
	return registries{npm: strings.TrimSuffix(npm, "/"), pypi: "https://pypi.org"}.resolve(ctx, language)
}

func (r registries) resolve(ctx context.Context, language string) (Versions, error) {
	var v Versions
	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() (err error) {
		v.HolonUI, err = r.newestNPM(ctx, typegen.NodePackages[typegen.TargetUI])
		return err
	})
	switch language {
	case typegen.TargetTS:
		group.Go(func() (err error) {
			v.HolonSH, err = r.newestNPM(ctx, typegen.NodePackages[typegen.TargetTS])
			return err
		})
	case typegen.TargetPy:
		group.Go(func() (err error) {
			v.HolonPy, err = r.newestPyPI(ctx, typegen.PythonRuntime)
			return err
		})
	}
	return v, group.Wait()
}

func (r registries) newestNPM(ctx context.Context, pkg string) (string, error) {
	var packument struct {
		Versions map[string]typegen.PackageJSON
	}
	if err := get(ctx, r.npm+"/"+url.PathEscape(pkg), &packument); err != nil {
		return "", fmt.Errorf("npm %s: %w", pkg, err)
	}
	newest := ""
	for version, manifest := range packument.Versions {
		if manifest.Holon.ContractVersion == typegen.ContractVersion && semver.IsValid("v"+version) &&
			(newest == "" || semver.Compare("v"+version, "v"+newest) > 0) {
			newest = version
		}
	}
	if newest == "" {
		return "", fmt.Errorf("npm %s: no release reads the clients this holon writes (holon.contractVersion %d)", pkg, typegen.ContractVersion)
	}
	return newest, nil
}

func (r registries) newestPyPI(ctx context.Context, pkg string) (string, error) {
	var project struct{ Info struct{ Version string } }
	if err := get(ctx, r.pypi+"/pypi/"+url.PathEscape(pkg)+"/json", &project); err != nil {
		return "", fmt.Errorf("PyPI %s: %w", pkg, err)
	}
	if project.Info.Version == "" {
		return "", fmt.Errorf("PyPI %s: no release", pkg)
	}
	return project.Info.Version, nil
}

func get(ctx context.Context, location string, value any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return err
	}
	// The full document: npm's abbreviated one leaves out the holon field.
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", location, response.Status)
	}
	data, err := clientutil.ReadLimited(response.Body, 16<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
