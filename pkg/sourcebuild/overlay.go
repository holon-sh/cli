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

// PackDirectory writes a source overlay from a private, writable snapshot.
// It writes the plan and optional build instructions into the snapshot before packing it.
func PackDirectory(ctx context.Context, dst io.Writer, root string, plan runtimecontract.Plan, build *runtimecontract.Build) (ocispec.Descriptor, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer func() { _ = dir.Close() }()
	if err := writeInstructions(dir, runtimecontract.PlanFile, plan); err != nil {
		return ocispec.Descriptor{}, err
	}
	if build != nil {
		if err := writeInstructions(dir, runtimecontract.BuildFile, *build); err != nil {
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
	var size byteCount
	compressed, err := zstd.NewWriter(io.MultiWriter(dst, digester.Hash(), &size), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := errors.Join(fsutil.WriteTar(ctx, stable, compressed), compressed.Close()); err != nil {
		return ocispec.Descriptor{}, err
	}
	return ocispec.Descriptor{MediaType: runtimecontract.OverlayMediaType, Digest: digester.Digest(), Size: int64(size)}, nil
}

func writeInstructions(root *os.Root, name string, instructions any) error {
	data, err := json.Marshal(instructions)
	if err != nil {
		return err
	}
	target := filepath.FromSlash(name)
	if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return root.WriteFile(target, data, 0o644)
}

func stableStat(_ string, s *fstypes.Stat) fsutil.MapResult {
	s.ModTime, s.Uid, s.Gid, s.Xattrs = 0, 0, 0, nil
	return fsutil.MapResultKeep
}

type byteCount int64

func (count *byteCount) Write(data []byte) (int, error) {
	*count += byteCount(len(data))
	return len(data), nil
}
