package installation

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/utils/ptr"
	"oras.land/oras-go/v2/content"

	"github.com/holon-sh/cli/pkg/appartifact"
	"github.com/holon-sh/protocol"
)

func buildImage(t *testing.T, marker string) buildApplication {
	t.Helper()
	return func(ctx context.Context, request buildRequest, progress io.Writer) (string, error) {
		if _, err := io.WriteString(progress, "fixture build output\n"); err != nil {
			return "", err
		}
		fixture, source := buildRegistry(t)
		config := ocispec.ImageConfig{
			User: "10001:10001", WorkingDir: "/app", Entrypoint: []string{"/bin/sh", "-c"},
			Cmd: []string{"exec node main.mjs"}, Labels: map[string]string{"test.build": marker},
		}
		var children []ocispec.Descriptor
		for _, platform := range request.Platforms {
			children = append(children, addTestImage(t, fixture, strings.TrimPrefix(platform, "linux/"), config, nil))
		}
		descriptor := addTestIndex(fixture, children...)
		var image string
		err := request.upload.run(ctx, func(grant protocol.UploadGrant) (err error) {
			image, err = importImage(ctx, strings.TrimPrefix(source.URL, "https://")+"/source@"+descriptor.Digest.String(),
				grant.Registry, grant.Repositories[request.app.Name], grant.Grant, request.httpClient)
			return err
		})
		return image, err
	}
}

func buildRegistry(t *testing.T) (*imageRegistry, *httptest.Server) {
	t.Helper()
	fixture := newImageRegistry()
	server := httptest.NewTLSServer(fixture)
	t.Cleanup(server.Close)
	return fixture, server
}

func buildManifest(t *testing.T, root string) manifestSpec {
	t.Helper()
	sourceFiles(t, root, map[string]string{"package.json": sourcePackage, "server.js": "console.log(1)"})
	manifest := manifestSpec{
		Apps: []manifestApp{
			{
				Name: "alpha",
				Path: root,
				Access: &protocol.AppAccess{
					Server: &protocol.AccessRule{},
				},
			},
		},
	}
	resolved, err := resolveManifestGraph(t.Context(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func prepareBuild(t *testing.T, manifest manifestSpec) manifestSpec {
	t.Helper()
	prepared, cleanup, err := prepareManifestTypes(t.Context(), manifest, buildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return prepared
}

func buildGrant(server *httptest.Server) protocol.UploadGrant {
	return protocol.UploadGrant{Grant: "grant-secret", Registry: strings.TrimPrefix(server.URL, "https://"), Repositories: map[string]string{"alpha": "holon/upload/alpha"}}
}

func sourceFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const sourcePackage = `{"private":true,"engines":{"node":">=22.20.0 <23"}}`

func TestSourceBuildAndUpload(t *testing.T) {
	root := t.TempDir()
	document := descriptorDocument("alpha")
	document["icons"] = []any{map[string]any{"src": "https://example.com/icon.png", "theme": "light"}}
	writeDescriptor(t, filepath.Join(root, "server.json"), document)
	manifest := buildManifest(t, root)
	if err := os.Remove(filepath.Join(root, "server.js")); err != nil {
		t.Fatal(err)
	}
	sourceFiles(t, root, map[string]string{
		"main.mjs": "console.log(1)", ".holon/app.txt": "application file",
		"package.json":      `{"main":"main.mjs"}`,
		".dockerignore":     "excluded/\n!excluded/keep.txt\n!node_modules/forbidden\n",
		"excluded/keep.txt": "yes", "excluded/no.txt": "no",
		"node_modules/forbidden": "no", ".git/config": "no", "script": "hello",
		".venv/pyvenv.cfg": "no", "__pycache__/server.pyc": "no",
	})
	if err := os.Chmod(filepath.Join(root, "script"), 0o755); err != nil {
		t.Fatal(err)
	}
	modified := time.Unix(1_700_000_000, 123)
	if err := os.Chtimes(filepath.Join(root, "script"), modified, modified); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("script", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	_, server := buildRegistry(t)
	build := buildImage(t, "source")
	options := buildOptions{HTTPClient: server.Client(), plane: protocol.DevPlane}
	options.builder = func(ctx context.Context, request buildRequest, progress io.Writer) (string, error) {
		snapshot := request.Source
		if err := os.WriteFile(filepath.Join(root, "main.mjs"), []byte("console.log(2)"), 0o600); err != nil {
			return "", err
		}
		t.Run("build input", func(t *testing.T) {
			for name, want := range map[string]string{
				"main.mjs": "console.log(1)", ".holon/app.txt": "application file",
				"package.json":      `{"main":"main.mjs"}`,
				"excluded/keep.txt": "yes", "script": "hello",
			} {
				got, err := os.ReadFile(filepath.Join(snapshot, name))
				if err != nil || string(got) != want {
					t.Fatalf("build input %s = %q, want %q: %v", name, got, want, err)
				}
			}
			for _, name := range []string{"excluded/no.txt", "node_modules/forbidden", ".git/config", ".dockerignore", ".venv", "__pycache__"} {
				if _, err := os.Lstat(filepath.Join(snapshot, name)); !os.IsNotExist(err) {
					t.Fatalf("excluded file reached builder: %s: %v", name, err)
				}
			}
			info, err := os.Stat(filepath.Join(snapshot, "script"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o755 {
				t.Fatalf("executable permissions = %o, want 755", info.Mode().Perm())
			}
			if !info.ModTime().Equal(modified) {
				t.Fatalf("modification time = %v, want %v", info.ModTime(), modified)
			}
			if link, err := os.Readlink(filepath.Join(snapshot, "link")); err != nil || link != "script" {
				t.Fatalf("source symlink = %q, want script: %v", link, err)
			}
		})
		return build(ctx, request, progress)
	}

	grant := buildGrant(server)
	manifest.Apps[0].resolved.Env = map[string]protocol.Input{
		"DEPLOYMENT_VALUE": {Value: ptr.To("only-for-this-deployment")},
		"TOKEN":            {IsSecret: true, IsRequired: true},
	}
	manifest = prepareBuild(t, manifest)
	apps, err := buildAndPush(t.Context(), manifest, &imageUpload{grant: grant}, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || !strings.HasPrefix(apps[0].Image, grant.Registry+"/holon/upload/alpha@sha256:") {
		t.Fatalf("unexpected image: %#v", apps)
	}
	repository, err := imageRepository(grant.Registry+"/"+grant.Repositories["alpha"], grant.Grant, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := repository.Resolve(t.Context(), appartifact.DevelopmentTag)
	if err != nil {
		t.Fatal(err)
	}
	record, executable, err := appartifact.Read(t.Context(), repository, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if record.AppName != "alpha" || record.ServerName != "io.github.acme/alpha" || !strings.HasSuffix(apps[0].Image, "@"+executable.Digest.String()) {
		t.Fatalf("published build: %+v", record)
	}
	if input := record.Runtime.Env["DEPLOYMENT_VALUE"]; !input.IsRequired || input.Value != nil || input.Default != nil {
		t.Fatal("deployment value escaped into shared metadata")
	}
	if len(record.Runtime.Icons) != 1 || record.Runtime.Icons[0].Source != "https://example.com/icon.png" || record.Runtime.Icons[0].Theme != "light" {
		t.Fatalf("build icon metadata lost: %+v", record.Runtime.Icons)
	}
	if input := record.Runtime.Env["TOKEN"]; !input.IsSecret || !input.IsRequired {
		t.Fatal("secret declaration was lost")
	}
}

func TestSourceRejectsEscapingSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	manifest := buildManifest(t, root)
	sourceFiles(t, outside, map[string]string{"file": ""})
	if err := os.Symlink(filepath.Join(outside, "file"), filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareManifestTypes(t.Context(), manifest, buildOptions{}); err == nil || !strings.Contains(err.Error(), "symlink escapes application") {
		t.Fatalf("escaping symlink: %v", err)
	}
}

func publishedJSON(t *testing.T, source content.Fetcher, descriptor ocispec.Descriptor, value any) {
	t.Helper()
	data, err := content.FetchAll(t.Context(), source, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}
