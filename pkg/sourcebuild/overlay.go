package sourcebuild

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"

	"github.com/holon-sh/protocol/runtimecontract"
)

// PackDirectory writes a source overlay from a private snapshot to an empty file.
// It writes the plan and optional build instructions into the snapshot before packing it.
func PackDirectory(ctx context.Context, file *os.File, root string, plan runtimecontract.Plan, build *runtimecontract.Build) (ocispec.Descriptor, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer func() { _ = dir.Close() }()
	if err := dir.MkdirAll(filepath.Dir(runtimecontract.PlanFile), 0o755); err != nil {
		return ocispec.Descriptor{}, err
	}
	planData, _ := json.Marshal(plan)
	if err := dir.WriteFile(runtimecontract.PlanFile, planData, 0o644); err != nil {
		return ocispec.Descriptor{}, err
	}
	if build != nil {
		buildData, _ := json.Marshal(build)
		if err := dir.WriteFile(runtimecontract.BuildFile, buildData, 0o644); err != nil {
			return ocispec.Descriptor{}, err
		}
	}
	source, err := fsutil.NewFS(root)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	stable, err := fsutil.NewFilterFS(source, &fsutil.FilterOpt{Map: stableStat})
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	digester := digest.SHA256.Digester()
	compressed, err := zstd.NewWriter(io.MultiWriter(file, digester.Hash()), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := errors.Join(fsutil.WriteTar(ctx, stable, compressed), compressed.Close()); err != nil {
		return ocispec.Descriptor{}, err
	}
	size, err := file.Seek(0, io.SeekCurrent)
	return ocispec.Descriptor{MediaType: runtimecontract.OverlayMediaType, Digest: digester.Digest(), Size: size}, err
}

func stableStat(_ string, s *fstypes.Stat) fsutil.MapResult {
	s.ModTime, s.Uid, s.Gid, s.Xattrs = 0, 0, 0, nil
	return fsutil.MapResultKeep
}
