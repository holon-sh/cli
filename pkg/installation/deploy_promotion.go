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

// These annotations on a source image's index name what the image was built from; see imageKeys.
const (
	promotionAnnotation = "sh.holon.promotion"
	inputsAnnotation    = "sh.holon.inputs"
)

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

// imageKeys name what an app's image is built from, by the index annotation that publishes each. The
// promotionAnnotation key names everything: its source snapshot and those of the apps it requires, whose
// clients it carries, the build architectures, and this executable, which supplies the launcher and the
// build. The inputsAnnotation key names the same but for the app's own server source, which a reload can
// replace. They are nil when any of these is unknown, such as a required app without source.
func imageKeys(app manifestApp, fingerprints map[string]string, architectures []string) map[string]string {
	executable := executableDigest()
	names := append([]string{app.Name}, app.resolved.Requires...)
	if executable == "" || slices.ContainsFunc(names, func(name string) bool { return fingerprints[name] == "" }) {
		return nil
	}
	platforms := strings.Join(slices.Sorted(slices.Values(buildArchitectures(architectures))), ",")
	keys := map[string]string{}
	for annotation, own := range map[string]string{promotionAnnotation: fingerprints[app.Name], inputsAnnotation: app.resolved.types.inputs} {
		hash := sha256.New()
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%s\x00", executable, platforms, app.Name, own)
		for _, name := range app.resolved.Requires {
			_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", name, fingerprints[name])
		}
		keys[annotation] = hex.EncodeToString(hash.Sum(nil))
	}
	return keys
}

// reusableImage returns the development session image, among images, whose annotation is key, read from the
// repository this build pushes to, or "" when none is.
func reusableImage(ctx context.Context, app manifestApp, annotation, key string, images []string, upload *imageUpload, options buildOptions) string {
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
			if err == nil && ociimage.FetchJSON(ctx, repository, descriptor, &index) == nil && index.Annotations[annotation] == key {
				found = image
				return nil
			}
		}
		return nil
	})
	return found
}
