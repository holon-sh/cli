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

// cliPackage is the package that installs holon into a Python or TypeScript project.
const cliPackage = "holon-types"

// Resolve picks the SDK releases a new app in language pins, from npm's registry, or the one
// npm_config_registry names, and PyPI: on npm, the newest release that reads the clients this holon
// writes (holon.contractVersion); on PyPI, the newest release. The app's holon-types is this holon's
// release (cliVersion), or the newest release when this holon is not one.
func Resolve(ctx context.Context, language, cliVersion string) (Versions, error) {
	npm := cmp.Or(os.Getenv("npm_config_registry"), os.Getenv("NPM_CONFIG_REGISTRY"), "https://registry.npmjs.org")
	return registries{npm: strings.TrimSuffix(npm, "/"), pypi: "https://pypi.org"}.resolve(ctx, language, cliVersion)
}

func (r registries) resolve(ctx context.Context, language, cliVersion string) (Versions, error) {
	var v Versions
	// A release is a version without build metadata, such as 0.0.1-dev.19; local builds are not.
	release := semver.IsValid("v"+cliVersion) && semver.Build("v"+cliVersion) == ""
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
		group.Go(func() (err error) {
			if release {
				v.HolonTypes = cliVersion
				return nil
			}
			v.HolonTypes, err = r.newestNPMRelease(ctx, cliPackage)
			return err
		})
	case typegen.TargetPy:
		group.Go(func() (err error) {
			v.HolonPy, err = r.newestPyPI(ctx, typegen.PythonRuntime)
			return err
		})
		group.Go(func() (err error) {
			if release {
				v.HolonTypes = pythonVersion(cliVersion)
				return nil
			}
			v.HolonTypes, err = r.newestPyPI(ctx, cliPackage)
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

// newestNPMRelease is pkg's newest release on npm.
func (r registries) newestNPMRelease(ctx context.Context, pkg string) (string, error) {
	var packument struct{ Versions map[string]json.RawMessage }
	if err := get(ctx, r.npm+"/"+url.PathEscape(pkg), &packument); err != nil {
		return "", fmt.Errorf("npm %s: %w", pkg, err)
	}
	newest := ""
	for version := range packument.Versions {
		if semver.IsValid("v"+version) && (newest == "" || semver.Compare("v"+version, "v"+newest) > 0) {
			newest = version
		}
	}
	if newest == "" {
		return "", fmt.Errorf("npm %s: no release", pkg)
	}
	return newest, nil
}

// pythonVersion spells a holon release as PyPI publishes it: 0.0.1-dev.19 is 0.0.1.dev19.
func pythonVersion(version string) string {
	return strings.Replace(version, "-dev.", ".dev", 1)
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
