package appartifact_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/holon-sh/cli/pkg/appartifact"
	"github.com/holon-sh/cli/pkg/ociimage"
)

func TestSourceLayer(t *testing.T) {
	ctx := t.Context()
	layer := func(name string) ocispec.Descriptor {
		return content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, []byte(name))
	}
	system, source, bridge := layer("system"), layer("source"), layer("bridge")
	config, _ := json.Marshal(ocispec.Image{History: []ocispec.History{{CreatedBy: "ADD system /"}, {CreatedBy: `CMD ["sh"]`, EmptyLayer: true}}})
	configDescriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, config)
	development, _ := json.Marshal(ocispec.Manifest{Config: configDescriptor, Layers: []ocispec.Descriptor{system}})
	by := map[digest.Digest]string{source.Digest: "holon deploy", bridge.Digest: "holon stdio bridge"}
	for _, test := range []struct {
		name     string
		appended []ocispec.Descriptor
		indexed  bool
		want     digest.Digest
	}{
		{"bridge on top", []ocispec.Descriptor{source, bridge}, true, source.Digest},
		{"nobody froze", nil, true, ""},
		{"plain manifest", nil, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := memory.New()
			if err := store.Push(ctx, configDescriptor, bytes.NewReader(config)); err != nil {
				t.Fatal(err)
			}
			image, err := ociimage.Edit(ctx, store, store, ocispec.MediaTypeImageManifest, development, func(image *ociimage.Image) error {
				for _, layer := range test.appended {
					image.AppendLayer(layer, layer.Digest, by[layer.Digest])
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if test.indexed {
				image.Platform = &ocispec.Platform{OS: "linux", Architecture: "amd64"}
				if image, err = appartifact.PushIndex(ctx, store, "", []ocispec.Descriptor{image}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := appartifact.SourceLayer(ctx, store, image)
			if got.Digest != test.want || errors.Is(err, appartifact.ErrNoSource) != (test.want == "") {
				t.Fatalf("SourceLayer = %s, %v; want %s", got.Digest, err, test.want)
			}
		})
	}
}
