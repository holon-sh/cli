package appartifact_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/holon-sh/cli/pkg/appartifact"
	"github.com/holon-sh/cli/pkg/ociimage"
)

func TestSourceLayer(t *testing.T) {
	ctx := t.Context()
	store := memory.New()
	layer := func(name string) ocispec.Descriptor {
		return content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, []byte(name))
	}
	system, dependencies, source, bridge := layer("system"), layer("dependencies"), layer("source"), layer("bridge")
	config, _ := json.Marshal(ocispec.Image{History: []ocispec.History{{CreatedBy: "ADD system /"}, {CreatedBy: `CMD ["sh"]`, EmptyLayer: true}}})
	configDescriptor, err := oras.PushBytes(ctx, store, ocispec.MediaTypeImageConfig, config)
	if err != nil {
		t.Fatal(err)
	}
	development, _ := json.Marshal(ocispec.Manifest{Config: configDescriptor, Layers: []ocispec.Descriptor{system}})
	by := map[digest.Digest]string{dependencies.Digest: appartifact.FrozenBy, source.Digest: appartifact.FrozenBy, bridge.Digest: "holon stdio bridge"}
	for _, test := range []struct {
		name     string
		appended []ocispec.Descriptor
		want     digest.Digest
	}{
		{"dependencies and source", []ocispec.Descriptor{dependencies, source}, source.Digest},
		{"source only", []ocispec.Descriptor{source}, source.Digest},
		{"bridge on top", []ocispec.Descriptor{dependencies, source, bridge}, source.Digest},
		{"nobody froze", nil, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			image, err := ociimage.Edit(ctx, store, store, ocispec.MediaTypeImageManifest, development, func(image *ociimage.Image) error {
				for _, layer := range test.appended {
					image.AppendLayer(layer, layer.Digest, by[layer.Digest])
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			image.Platform = &ocispec.Platform{OS: "linux", Architecture: "amd64"}
			index, err := appartifact.PushIndex(ctx, store, "", []ocispec.Descriptor{image})
			if err != nil {
				t.Fatal(err)
			}
			got, err := appartifact.SourceLayer(ctx, store, index)
			if got.Digest != test.want || errors.Is(err, appartifact.ErrNoSource) != (test.want == "") {
				t.Fatalf("SourceLayer = %s, %v; want %s", got.Digest, err, test.want)
			}
		})
	}
}
