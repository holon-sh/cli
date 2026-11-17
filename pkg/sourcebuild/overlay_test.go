package sourcebuild_test

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"

	"github.com/holon-sh/cli/pkg/sourcebuild"
	"github.com/holon-sh/protocol/runtimecontract"
)

func TestPackGeneratedSource(t *testing.T) {
	root := t.TempDir()
	source := []byte("export default handler;")
	if err := os.WriteFile(filepath.Join(root, "server.ts"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	plan := runtimecontract.Plan{Install: runtimecontract.Install{Commands: [][]string{{"npm", "install"}}}}
	build := runtimecontract.Build{Freeze: &runtimecontract.Freeze{Dependencies: []string{"node_modules"}}}

	var archive bytes.Buffer
	descriptor, err := sourcebuild.PackDirectory(t.Context(), &archive, root, plan, &build)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.MediaType != runtimecontract.OverlayMediaType || descriptor.Digest != digest.FromBytes(archive.Bytes()) || descriptor.Size != int64(archive.Len()) {
		t.Fatalf("descriptor does not describe the packed source: %#v", descriptor)
	}
	compressed, err := zstd.NewReader(&archive)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	files := map[string][]byte{}
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			files[header.Name], err = io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(files) != 3 || !bytes.Equal(files["server.ts"], source) {
		t.Fatalf("unexpected source files in overlay: %q", files)
	}
	var actualPlan runtimecontract.Plan
	var actualBuild runtimecontract.Build
	if err := errors.Join(
		json.Unmarshal(files[runtimecontract.PlanFile], &actualPlan),
		json.Unmarshal(files[runtimecontract.BuildFile], &actualBuild),
	); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualPlan, plan) || !reflect.DeepEqual(actualBuild, build) {
		t.Fatalf("build received plan %#v and build %#v", actualPlan, actualBuild)
	}
}
