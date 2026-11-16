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

const cliPackage = "holon-types"

// Resolve picks the newest SDK releases a new app in language pins, from npm's registry, or the one
// npm_config_registry names, and PyPI.
func Resolve(ctx context.Context, language, cliVersion string) (Versions, error) {
	npm := cmp.Or(os.Getenv("npm_config_registry"), os.Getenv("NPM_CONFIG_REGISTRY"), "https://registry.npmjs.org")
	return registries{npm: strings.TrimSuffix(npm, "/"), pypi: "https://pypi.org"}.resolve(ctx, language, cliVersion)
}

func (r registries) resolve(ctx context.Context, language, cliVersion string) (Versions, error) {
	var v Versions
	if semver.IsValid("v"+cliVersion) && semver.Build("v"+cliVersion) == "" {
		v.CLI = cliVersion
	}
	group, ctx := errgroup.WithContext(ctx)
	lookup := func(version *string, location string) {
		if *version == "" {
			group.Go(func() (err error) {
				*version, err = newest(ctx, location)
				return err
			})
		}
	}
	npm := func(pkg string) string { return r.npm + "/" + url.PathEscape(pkg) }
	pypi := func(pkg string) string { return r.pypi + "/pypi/" + url.PathEscape(pkg) + "/json" }
	lookup(&v.UI, npm(typegen.NodePackages[typegen.TargetUI]))
	switch language {
	case typegen.TargetTS:
		lookup(&v.SDK, npm(typegen.NodePackages[typegen.TargetTS]))
		lookup(&v.CLI, npm(cliPackage))
	case typegen.TargetPy:
		lookup(&v.SDK, pypi(typegen.PythonRuntime))
		lookup(&v.CLI, pypi(cliPackage))
	}
	return v, group.Wait()
}

func newest(ctx context.Context, location string) (string, error) {
	data, err := clientutil.Get(ctx, client, location, 16<<20)
	if err != nil {
		return "", err
	}
	var document struct {
		DistTags struct{ Latest string } `json:"dist-tags"`
		Info     struct{ Version string }
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return "", fmt.Errorf("%s: %w", location, err)
	}
	if version := cmp.Or(document.DistTags.Latest, document.Info.Version); version != "" {
		return version, nil
	}
	return "", fmt.Errorf("%s: no release", location)
}
