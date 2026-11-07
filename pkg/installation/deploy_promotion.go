package installation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
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
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
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
	if len(architectures) == 0 {
		architectures = []string{"amd64", "arm64"}
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", executable, strings.Join(slices.Sorted(slices.Values(architectures)), ","))
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
	fingerprints := map[string]string{}
	for _, app := range resolved.Apps {
		if app.Path == "" {
			if app.resolved.Remote == nil {
				return protocol.AppsRequestFor[protocol.App]{}, false
			}
			continue
		}
		if fingerprints[app.Name], err = snapshotSource(app, ""); err != nil {
			return protocol.AppsRequestFor[protocol.App]{}, false
		}
	}
	var sessions struct {
		Items []protocol.SessionResponse `json:"items"`
	}
	if err := manager.Call(ctx, protocol.DevPlane, toolListSessions, struct{}{}, &sessions); err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, false
	}
	grant, err := imageDestination(ctx, manager, protocol.ProductionName, resolved)
	if err != nil || validateUploadGrant(grant, resolved) != nil {
		return protocol.AppsRequestFor[protocol.App]{}, false
	}
	desired := make([]protocol.App, 0, len(resolved.Apps))
	for _, app := range resolved.Apps {
		declaration := app.resolved.App
		if app.Path != "" {
			key := promotionKey(app, fingerprints, manager.info.BuildArchitectures)
			image := promotableImage(ctx, app, key, sessions.Items, grant, options)
			if key == "" || image == "" {
				return protocol.AppsRequestFor[protocol.App]{}, false
			}
			declaration.Image = image
		}
		desired = append(desired, declaration)
	}
	options.output.status("", "Reusing development images", progressWorking)
	return protocol.AppsRequestFor[protocol.App]{Apps: desired}, true
}

// promotableImage finds a development image of app carrying key in the production upload repository,
// and publishes its build record as a release when the installation keeps a catalog.
func promotableImage(
	ctx context.Context, app manifestApp, key string, sessions []protocol.SessionResponse, grant protocol.UploadGrant, options buildOptions,
) string {
	name := grant.Registry + "/" + grant.Repositories[app.Name]
	repository, err := imageRepository(name, grant.Grant, options.HTTPClient)
	if err != nil {
		return ""
	}
	for _, session := range sessions {
		for _, candidate := range session.Apps {
			if candidate.Name != app.Name || candidate.Image == "" {
				continue
			}
			if named, err := ociimage.Parse(candidate.Image); err != nil || named.Name() != name {
				continue
			}
			descriptor, annotations, err := fetchIndex(ctx, repository, candidate.Image)
			if err != nil || annotations[promotionAnnotation] != key {
				continue
			}
			if grant.Grant == "" || releaseDevelopmentRecord(ctx, repository, app, descriptor) == nil {
				return candidate.Image
			}
		}
	}
	return ""
}

func fetchIndex(ctx context.Context, repository *remote.Repository, reference string) (ocispec.Descriptor, map[string]string, error) {
	descriptor, data, err := fetchJSON(ctx, repository, reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	var index ocispec.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return descriptor, index.Annotations, nil
}

func fetchJSON(ctx context.Context, repository *remote.Repository, reference string) (ocispec.Descriptor, []byte, error) {
	descriptor, err := repository.Resolve(ctx, reference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	data, err := content.FetchAll(ctx, repository, descriptor)
	return descriptor, data, err
}

// releaseDevelopmentRecord tags the build record a development build published for image as a release,
// once it is the record this deploy would publish. Its server identity comes from the same source.
func releaseDevelopmentRecord(ctx context.Context, repository *remote.Repository, app manifestApp, image ocispec.Descriptor) error {
	descriptor, data, err := fetchJSON(ctx, repository, appartifact.DevelopmentTag)
	if err != nil {
		return err
	}
	var index ocispec.Index
	if err := json.Unmarshal(data, &index); err != nil || len(index.Manifests) != 2 || index.Manifests[0].Digest != image.Digest {
		return errors.New("the development build record names another image")
	}
	_, data, err = fetchJSON(ctx, repository, index.Manifests[1].Digest.String())
	var metadata ocispec.Manifest
	if err != nil || json.Unmarshal(data, &metadata) != nil || len(metadata.Layers) != 1 {
		return errors.New("the development build record is unreadable")
	}
	data, err = content.FetchAll(ctx, repository.Blobs(), metadata.Layers[0])
	var record protocol.BuildRecord
	if err != nil || json.Unmarshal(data, &record) != nil {
		return errors.New("the development build record is unreadable")
	}
	// JSON, which the record was read from, does not tell an empty list from an absent one.
	published, _ := json.Marshal(record.Runtime)
	wanted, _ := json.Marshal(mcpregistry.BuildRecord(app.resolved.App, app.resolved.catalogEnv, nil).Runtime)
	if !bytes.Equal(published, wanted) {
		return errors.New("the development build record describes another declaration")
	}
	return repository.Tag(ctx, descriptor, appartifact.ReleasePrefix+descriptor.Digest.Encoded())
}
