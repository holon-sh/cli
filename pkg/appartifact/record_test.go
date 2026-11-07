package appartifact

import (
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/holon-sh/protocol"
)

func TestReadReturnsThePublishedImageAndRecord(t *testing.T) {
	store := memory.New()
	image, err := oras.PushBytes(t.Context(), store, ocispec.MediaTypeImageIndex, []byte(`{"schemaVersion":2,"manifests":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	record := protocol.BuildRecord{
		SchemaVersion: 1, AppName: "orders", ServerName: "io.example/orders", ServerVersion: "1.2.0",
		Runtime: protocol.BuildRuntime{Port: 3000}, UnsupportedReasons: []string{},
	}
	if err := Publish(t.Context(), store, image, record, DevelopmentTag); err != nil {
		t.Fatal(err)
	}
	build, err := Read(t.Context(), store, DevelopmentTag)
	if err != nil {
		t.Fatal(err)
	}
	if build.Image.Digest != image.Digest || build.Record.AppName != record.AppName || build.Record.ServerName != record.ServerName {
		t.Fatalf("Read = %+v", build)
	}
	if err := Release(t.Context(), store, build.Metadata); err != nil {
		t.Fatal(err)
	}
	// A release of what a development build published is the release a deploy build would publish.
	released, err := Read(t.Context(), store, releaseTag(build.Metadata))
	if err != nil || released.Metadata.Digest != build.Metadata.Digest {
		t.Fatalf("release = %+v, %v", released, err)
	}
	if _, err := Read(t.Context(), store, image.Digest.String()); err == nil {
		t.Fatal("Read accepted an image that is not build metadata")
	}
}
