package scaffold

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/holon-sh/cli/pkg/typegen"
)

// Registries are where Resolve looks up SDK releases.
type Registries struct {
	NPM, PyPI string
	Client    *http.Client
}

// DefaultRegistries are npm's registry, or the one npm_config_registry names, and PyPI.
func DefaultRegistries() Registries {
	npm := "https://registry.npmjs.org"
	for _, name := range []string{"npm_config_registry", "NPM_CONFIG_REGISTRY"} {
		if value := os.Getenv(name); value != "" {
			npm = value
			break
		}
	}
	return Registries{NPM: strings.TrimSuffix(npm, "/"), PyPI: "https://pypi.org", Client: http.DefaultClient}
}

// Resolve picks the SDK releases a new app in language pins: for npm, the newest release that reads
// the clients this holon writes (holon.contractVersion); for PyPI, the newest release.
func Resolve(ctx context.Context, language string, r Registries) (Versions, error) {
	var v Versions
	var err error
	if v.HolonUI, err = r.npm(ctx, "holon-ui"); err != nil {
		return v, err
	}
	switch language {
	case "ts":
		v.HolonSH, err = r.npm(ctx, "holon-sh")
	case "py":
		v.HolonPy, err = r.pypi(ctx, "holon-sh")
	}
	return v, err
}

func (r Registries) npm(ctx context.Context, pkg string) (string, error) {
	var packument struct {
		Versions map[string]struct {
			Holon struct {
				ContractVersion int `json:"contractVersion"`
			} `json:"holon"`
		} `json:"versions"`
	}
	if err := r.get(ctx, r.NPM+"/"+url.PathEscape(pkg), &packument); err != nil {
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

func (r Registries) pypi(ctx context.Context, pkg string) (string, error) {
	var project struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := r.get(ctx, r.PyPI+"/pypi/"+url.PathEscape(pkg)+"/json", &project); err != nil {
		return "", fmt.Errorf("PyPI %s: %w", pkg, err)
	}
	if project.Info.Version == "" {
		return "", fmt.Errorf("PyPI %s: no release", pkg)
	}
	return project.Info.Version, nil
}

func (r Registries) get(ctx context.Context, location string, value any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := r.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", location, response.Status)
	}
	return json.NewDecoder(response.Body).Decode(value)
}
