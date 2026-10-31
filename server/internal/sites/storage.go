package sites

import (
	"context"
	"io"
	"io/fs"
	"math"
	"path/filepath"
)

// Check before reading the body. Keep space for a full replacement while the
// old folder is still on disk. Other processes can still consume disk space.
func (s *Sites) checkStorage(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	free, err := s.freeSpace(s.dir)
	if err != nil {
		return err
	}
	// Allow a filesystem block for each small file or directory as well.
	staging := uint64(s.MaxUnpacked) + uint64(s.MaxFiles)*4096
	if free < staging || free-staging < s.MinFreeSpace {
		return ErrStorageFull
	}
	if s.MaxStorage == 0 {
		return nil
	}
	remaining := s.MaxStorage
	err = filepath.WalkDir(s.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > remaining {
			return ErrStorageFull
		}
		remaining -= info.Size()
		return nil
	})
	if err != nil {
		return err
	}
	if s.MaxUnpacked > remaining {
		return ErrStorageFull
	}
	return nil
}

func availableBytes(blocks, size uint64) uint64 {
	if size != 0 && blocks > math.MaxUint64/size {
		return math.MaxUint64
	}
	return blocks * size
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if cancelled := r.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	return n, err
}
