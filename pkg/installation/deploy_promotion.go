package installation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

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

// reusableImage returns the development session image, among images, that a build of app with key would
// produce, read from the repository this build pushes to, or "" when none is.
func reusableImage(ctx context.Context, app manifestApp, key string, images []string, upload *imageUpload, options buildOptions) string {
	if key == "" || len(images) == 0 {
		return ""
	}
	var found string
	_ = buildRequest{app: app, upload: upload, httpClient: options.HTTPClient}.publish(ctx, func(repository *remote.Repository, _ protocol.UploadGrant) error {
		name := repository.Reference.Registry + "/" + repository.Reference.Repository
		for _, image := range images {
			if named, err := ociimage.Parse(image); err != nil || named.Name() != name {
				continue
			}
			descriptor, err := repository.Resolve(ctx, image)
			var index ocispec.Index
			if err == nil && ociimage.FetchJSON(ctx, repository, descriptor, &index) == nil && index.Annotations[promotionAnnotation] == key {
				found = image
				return nil
			}
		}
		return nil
	})
	return found
}
