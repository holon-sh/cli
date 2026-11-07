package installation

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/holon-sh/cli/pkg/appartifact"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/ociimage"
	"github.com/holon-sh/protocol"
)

// promotionAnnotation, on a source image's index, names what the image was built from.
const promotionAnnotation = "sh.holon.promotion"

var executableDigest = sync.OnceValue(func() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	sum, err := digest.SHA256.FromReader(file)
	if err != nil {
		return ""
	}
	return sum.Encoded()
})

// promotionKey names everything an app's image is built from: its source snapshot and those of the apps
// it requires, whose clients it carries, the build architectures, and this executable, which supplies the
// launcher and the build. It is "" when any of them is unknown, such as a required app without source.
func promotionKey(app manifestApp, fingerprints map[string]string, architectures []string) string {
	executable := executableDigest()
	names := append([]string{app.Name}, app.resolved.Requires...)
	if executable == "" || slices.ContainsFunc(names, func(name string) bool { return fingerprints[name] == "" }) {
		return ""
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", executable, strings.Join(slices.Sorted(slices.Values(buildArchitectures(architectures))), ","))
	for _, name := range names {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", name, fingerprints[name])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// promoteDevelopment deploys the images of a development session that were built from the working tree
// as it is, instead of building them again. It reports false, and deploy builds, when any app differs
// or cannot be checked, since an image is only reused when it is exactly what a build would produce.
func promoteDevelopment(
	ctx context.Context, manager *Client, manifest manifestSpec, options buildOptions, apps ...string,
) (protocol.AppsRequestFor[protocol.App], bool) {
	resolved, err := resolveManifestGraph(ctx, manifest, apps...)
	if err != nil || protocol.AccessPolicy(resolved.declarations(), manager.info.OAuth != nil) != nil {
		return protocol.AppsRequestFor[protocol.App]{}, false
	}
	sessions, err := scopeSessions(ctx, manager, "")
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, false
	}
	candidates := map[string][]string{}
	for _, session := range sessions {
		for _, app := range session.Apps {
			if app.Image != "" {
				candidates[app.Name] = append(candidates[app.Name], app.Image)
			}
		}
	}
	fingerprints := map[string]string{}
	for _, app := range resolved.Apps {
		switch {
		case app.Path != "" && len(candidates[app.Name]) != 0:
			if fingerprints[app.Name], err = snapshotSource(app, ""); err != nil {
				return protocol.AppsRequestFor[protocol.App]{}, false
			}
		case app.Path != "" || app.resolved.Remote == nil:
			return protocol.AppsRequestFor[protocol.App]{}, false
		}
	}
	keys := map[string]string{}
	for _, app := range resolved.Apps {
		if app.Path == "" {
			continue
		}
		if keys[app.Name] = promotionKey(app, fingerprints, manager.info.BuildArchitectures); keys[app.Name] == "" {
			return protocol.AppsRequestFor[protocol.App]{}, false
		}
	}
	desired := make([]protocol.App, len(resolved.Apps))
	err = newImageUpload(manager, protocol.ProductionName, resolved).run(ctx, func(grant protocol.UploadGrant) error {
		for i, app := range resolved.Apps {
			desired[i] = app.resolved.App
			if app.Path == "" {
				continue
			}
			image, err := promotableImage(ctx, app, keys[app.Name], candidates[app.Name], grant, options)
			if err != nil || image == "" {
				return cmp.Or(err, errNotPromotable)
			}
			desired[i].Image = image
		}
		return nil
	})
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, false
	}
	options.output.status("", "Reusing development images", progressWorking)
	return protocol.AppsRequestFor[protocol.App]{Apps: desired}, true
}

var errNotPromotable = errors.New("no development image matches")

// promotableImage finds the image among images that carries key in the production upload repository,
// and publishes its build record as a release when the installation keeps a catalog.
func promotableImage(
	ctx context.Context, app manifestApp, key string, images []string, grant protocol.UploadGrant, options buildOptions,
) (string, error) {
	name := grant.Registry + "/" + grant.Repositories[app.Name]
	repository, err := imageRepository(name, grant.Grant, options.HTTPClient)
	if err != nil {
		return "", err
	}
	for _, image := range images {
		if named, err := ociimage.Parse(image); err != nil || named.Name() != name {
			continue
		}
		descriptor, err := repository.Resolve(ctx, image)
		var index ocispec.Index
		if err != nil || ociimage.FetchJSON(ctx, repository, descriptor, &index) != nil || index.Annotations[promotionAnnotation] != key {
			continue
		}
		if grant.Grant == "" {
			return image, nil
		}
		if released, err := releaseDevelopmentRecord(ctx, repository, app, descriptor); err != nil || released {
			return image, err
		}
	}
	return "", nil
}

// releaseDevelopmentRecord publishes, as a release, the build record a development build published for
// image, when it describes this deploy's declaration. Its server identity comes from the same source.
func releaseDevelopmentRecord(ctx context.Context, repository *remote.Repository, app manifestApp, image ocispec.Descriptor) (bool, error) {
	build, err := appartifact.Read(ctx, repository, appartifact.DevelopmentTag)
	if err != nil || build.Image.Digest != image.Digest {
		return false, nil
	}
	// JSON, which the record was read from, does not tell an empty list from an absent one.
	published, _ := json.Marshal(build.Record.Runtime)
	wanted, _ := json.Marshal(mcpregistry.BuildRecord(app.resolved.App, app.resolved.catalogEnv, nil).Runtime)
	if !bytes.Equal(published, wanted) {
		return false, nil
	}
	return true, appartifact.Release(ctx, repository, build.Metadata)
}
