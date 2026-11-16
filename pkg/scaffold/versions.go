package scaffold

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
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
	lookup := func(version *string, name, location string) {
		if *version == "" {
			group.Go(func() (err error) {
				if *version, err = newest(ctx, location); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				return nil
			})
		}
	}
	npm := func(pin *string, pkg string) { lookup(pin, "npm "+pkg, r.npm+"/"+url.PathEscape(pkg)) }
	pypi := func(pin *string, pkg string) { lookup(pin, "PyPI "+pkg, r.pypi+"/pypi/"+url.PathEscape(pkg)+"/json") }
	npm(&v.UI, typegen.NodePackages[typegen.TargetUI])
	switch language {
	case typegen.TargetTS:
		npm(&v.SDK, typegen.NodePackages[typegen.TargetTS])
		npm(&v.CLI, cliPackage)
	case typegen.TargetPy:
		pypi(&v.SDK, typegen.PythonRuntime)
		pypi(&v.CLI, cliPackage)
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
		return "", err
	}
	if version := cmp.Or(document.DistTags.Latest, document.Info.Version); version != "" {
		return version, nil
	}
	return "", errors.New("no release")
}
