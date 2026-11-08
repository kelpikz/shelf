package sites

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Publish unpacks a .tar.gz into a new folder and makes it the Site at path.
// A replace keeps the Visibility and the archive state of the Site.
func (s *Sites) Publish(ctx context.Context, path string, kind Kind, tarGz io.Reader) (Site, error) {
	if err := s.validateUpload(ctx, path, kind); err != nil {
		return Site{}, err
	}
	release, err := s.beginUpload()
	if err != nil {
		return Site{}, err
	}
	defer release()
	if err := s.prepareUploadStorage(ctx); err != nil {
		return Site{}, err
	}
	staged, err := s.stageUpload(ctx, path, kind, tarGz)
	if err != nil {
		return Site{}, err
	}
	defer staged.discard()
	return s.installSite(ctx, staged)
}

// validateUpload checks the path and kind before taking the upload slot.
func (s *Sites) validateUpload(ctx context.Context, path string, kind Kind) error {
	if _, _, err := s.Check(ctx, path); err != nil {
		return err
	}
	if kind != File && kind != Folder {
		return badUpload("unknown kind %q", kind)
	}
	return nil
}

// beginUpload takes the one upload slot without waiting. It returns
// ErrUploadBusy if another upload holds it. Call release when done.
func (s *Sites) beginUpload() (release func(), err error) {
	select {
	case s.upload <- struct{}{}:
		return func() { <-s.upload }, nil
	default:
		return nil, ErrUploadBusy
	}
}

// prepareUploadStorage makes the data dir and checks the free disk space.
func (s *Sites) prepareUploadStorage(ctx context.Context) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return s.checkStorage(ctx)
}

// A staged upload owns its folder until the database points to it.
type stagedUpload struct {
	path, file, folder, dir string
	kind                    Kind
	installed               bool
}

// stageUpload unpacks into a new <path>-<uuid> folder. The live Site does
// not change. On error or cancel, the folder is removed.
func (s *Sites) stageUpload(ctx context.Context, path string, kind Kind, tarGz io.Reader) (*stagedUpload, error) {
	upload := &stagedUpload{path: path, kind: kind, folder: path + "-" + uuid.NewString()}
	upload.dir = filepath.Join(s.dir, upload.folder)
	file, err := s.unpack(ctx, upload.dir, kind, contextReader{ctx, tarGz})
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		upload.discard()
		return nil, err
	}
	upload.file = file
	return upload, nil
}

// discard removes the staged folder unless installSite put it live.
func (u *stagedUpload) discard() {
	if !u.installed {
		os.RemoveAll(u.dir)
	}
}

// installSite switches the record before removing the old files. The lock
// covers installation and deletion, while unpacking stays outside the lock.
func (s *Sites) installSite(ctx context.Context, upload *stagedUpload) (Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, found, err := s.Get(ctx, upload.path)
	if err != nil {
		return Site{}, err
	}
	now := s.Now().Unix()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sites (path, kind, file, folder, visibility, archived, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?)
		 ON CONFLICT (path) DO UPDATE SET kind = excluded.kind, file = excluded.file,
		   folder = excluded.folder, updated_at = excluded.updated_at`,
		upload.path, upload.kind, upload.file, upload.folder, Private, now, now)
	if err != nil {
		return Site{}, err
	}
	// Keep the installed files even if the following read fails.
	upload.installed = true
	if found {
		s.removeFolder(old.folder)
	}
	site, _, err := s.Get(ctx, upload.path)
	return site, err
}
