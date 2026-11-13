package installation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

func registryPackageManifest(t *testing.T, registryType string) manifestSpec {
	t.Helper()
	manifest, err := parseManifest([]byte(`apps:
- name: echo
  registry:
    name: com.example/echo
    version: "1.0.0"
    package: {registryType: `+registryType+`}
  arguments:
    --workspace: legal
    --api-key: {secret: API_KEY}
  access: {server: {}}
`), "holon.yaml")
	if err != nil {
		t.Fatal(err)
	}
	app := &manifest.Apps[0]
	app.resolved.App = protocol.App{Name: app.Name, Access: app.Access}
	app.resolved.descriptor = &mcpregistry.Descriptor{
		Name: app.Registry.Name, Version: app.Registry.Version, Description: "Echo tools", Packages: []mcpregistry.Package{{
			RegistryType: registryType, Identifier: "echo", Version: "1.2.3", Transport: mcpregistry.Transport{Type: "stdio"},
			Environment: []json.RawMessage{json.RawMessage(`{"name":"API_TOKEN","isSecret":true,"isRequired":true}`)},
			PackageArguments: []json.RawMessage{
				json.RawMessage(`{"type":"named","name":"--workspace","isRequired":true}`),
				json.RawMessage(`{"type":"named","name":"--api-key","isRequired":true,"isSecret":true}`),
			},
		}},
	}
	if err := resolveApp(app); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestRegistryPackagesImportOnTheServerAndBindArguments(t *testing.T) {
	manifest := registryPackageManifest(t, "npm")
	h := newRunHarness(t)
	ref := protocol.AppBuildRef{Repository: "holon/importer/echo", ArtifactDigest: "sha256:" + strings.Repeat("a", 64)}
	image := "registry.example/holon/importer/echo@sha256:" + strings.Repeat("b", 64)
	h.control.Handle([]string{protocol.ToolImportRegistryPackage}, nil, func(w http.ResponseWriter, r *http.Request) {
		installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
		var input protocol.ImportRegistryPackageRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		selection := &protocol.PackageSelection{RegistryType: "npm", Identifier: "echo"}
		if input.App != "echo" || input.Registry.Name != "com.example/echo" || input.Registry.Version != "1.0.0" ||
			!reflect.DeepEqual(input.Registry.Package, selection) {
			t.Errorf("import request = %+v", input)
		}
		installationtest.WriteJSON(w, protocol.Import{ID: "12ab34cd", Phase: "Pending"})
	})
	h.control.Handle([]string{protocol.ToolGetImport}, map[string]string{"id": "12ab34cd"}, func(w http.ResponseWriter, r *http.Request) {
		installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
		installationtest.WriteJSON(w, protocol.Import{ID: "12ab34cd", Phase: "Succeeded", Build: &ref})
	})
	h.control.Handle([]string{protocol.ToolResolveAppBuild}, nil, func(w http.ResponseWriter, r *http.Request) {
		var input protocol.AppBuildRef
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input != ref {
			t.Errorf("resolve build = %+v", input)
		}
		installationtest.WriteJSON(w, protocol.ResolvedAppBuild{AppBuildRef: ref, Image: image, BuildRecord: protocol.BuildRecord{
			Runtime: protocol.BuildRuntime{Port: 8080},
		}})
	})
	h.start(t, nil)
	manager, err := NewClient(t.Context(), h.run.Auth)
	if err != nil {
		t.Fatal(err)
	}
	apps, err := buildManifestImages(t.Context(), manager, "", manifest, buildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.App{
		Name: "echo", Access: &protocol.AppAccess{Server: &protocol.AccessRule{}}, Image: image, Port: 8080, Imported: true,
		Args: []protocol.Template{"--workspace={WORKSPACE}", "--api-key={API_KEY}"},
		Env: map[string]protocol.Input{
			"API_TOKEN": {IsSecret: true, IsRequired: true}, "WORKSPACE": {Value: new("legal")}, "API_KEY": {IsRequired: true, IsSecret: true},
		},
	}
	if len(apps) != 1 {
		t.Fatalf("deployed apps = %+v", apps)
	}
	if diff := cmp.Diff(want, apps[0], cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("deployed app (-want +got):\n%s", diff)
	}
}

func TestRegistryPackageImportFailureAndCancellation(t *testing.T) {
	for _, phase := range []string{"Failed", "Running"} {
		t.Run(phase, func(t *testing.T) {
			manifest := registryPackageManifest(t, "npm")
			h := newRunHarness(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			h.control.Handle([]string{protocol.ToolImportRegistryPackage}, nil, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, protocol.Import{ID: "12ab34cd", Phase: "Pending"})
			})
			h.control.Handle([]string{protocol.ToolGetImport}, nil, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, protocol.Import{ID: "12ab34cd", Phase: phase, Message: "native-package runs an install script"})
				if phase != "Failed" {
					cancel()
				}
			})
			h.start(t, nil)
			manager, err := NewClient(t.Context(), h.run.Auth)
			if err != nil {
				t.Fatal(err)
			}
			_, err = buildManifestImages(ctx, manager, "", manifest, buildOptions{})
			if phase == "Failed" {
				if err == nil || !strings.Contains(err.Error(), "native-package runs an install script") {
					t.Fatalf("failure = %v", err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation = %v", err)
			}
		})
	}
}
