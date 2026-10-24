// Package sites keeps the Site records in SQLite and the Site files on the
// disk. All the rules about Sites live here.
package sites

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	File   Kind = "file"   // one file, served at /path
	Folder Kind = "folder" // a folder with index.html, served at /path/
)

type Visibility string

const (
	Public  Visibility = "public"
	Private Visibility = "private"
)

type Site struct {
	ID         int64
	Path       string
	Kind       Kind
	File       string // the file name of a one-file Site
	Visibility Visibility
	Archived   bool
	UpdatedAt  time.Time // the last upload

	folder string
}

// Sites keeps each Site's files in its own folder in dir. An upload goes into
// a new folder, and one database change switches the Site to it.
type Sites struct {
	db  *sql.DB
	dir string
	Now func() time.Time

	// Limits for one upload.
	MaxUpload   int64 // bytes of the .tar.gz
	MaxUnpacked int64 // bytes of all files after unpacking
	MaxFiles    int

	mu sync.Mutex // one Publish or Delete at a time
}

func New(db *sql.DB, dataDir string) *Sites {
	return &Sites{
		db: db, dir: filepath.Join(dataDir, "sites"), Now: time.Now,
		MaxUpload: 10 << 20, MaxUnpacked: 50 << 20, MaxFiles: 10_000,
	}
}

// Get gives the Site at path, if there is one.
func (s *Sites) Get(ctx context.Context, path string) (Site, bool, error) {
	var site Site
	var updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, path, kind, file, folder, visibility, archived, updated_at FROM sites WHERE path = ?`, path).
		Scan(&site.ID, &site.Path, &site.Kind, &site.File, &site.folder, &site.Visibility, &site.Archived, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, false, nil
	}
	if err != nil {
		return Site{}, false, err
	}
	site.UpdatedAt = time.Unix(updated, 0)
	return site, true, nil
}

// List gives all Sites, the newest upload first.
func (s *Sites) List(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, path, kind, file, folder, visibility, archived, updated_at FROM sites ORDER BY updated_at DESC, path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Site
	for rows.Next() {
		var site Site
		var updated int64
		if err := rows.Scan(&site.ID, &site.Path, &site.Kind, &site.File, &site.folder, &site.Visibility, &site.Archived, &updated); err != nil {
			return nil, err
		}
		site.UpdatedAt = time.Unix(updated, 0)
		list = append(list, site)
	}
	return list, rows.Err()
}

// Version changes with each upload. Serving uses it as the ETag.
func (site Site) Version() string { return site.folder }

// URL gives the address of the Site relative to the Sites host.
func (site Site) URL() string {
	if site.Kind == File {
		return "/" + site.Path
	}
	return "/" + site.Path + "/"
}

// Publish unpacks a .tar.gz into a new folder and makes it the Site at path.
// A replace keeps the Visibility and the archive state of the Site.
func (s *Sites) Publish(ctx context.Context, path string, kind Kind, tarGz io.Reader) (Site, error) {
	if _, _, err := s.Check(ctx, path); err != nil {
		return Site{}, err
	}
	if kind != File && kind != Folder {
		return Site{}, badUpload("unknown kind %q", kind)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return Site{}, err
	}
	folder := path + "-" + rand.Text()
	file, err := s.unpack(filepath.Join(s.dir, folder), kind, tarGz)
	if err != nil {
		os.RemoveAll(filepath.Join(s.dir, folder))
		return Site{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	old, found, err := s.Get(ctx, path)
	if err != nil {
		os.RemoveAll(filepath.Join(s.dir, folder))
		return Site{}, err
	}
	now := s.Now().Unix()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sites (path, kind, file, folder, visibility, archived, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?)
		 ON CONFLICT (path) DO UPDATE SET kind = excluded.kind, file = excluded.file,
		   folder = excluded.folder, updated_at = excluded.updated_at`,
		path, kind, file, folder, Private, now, now)
	if err != nil {
		os.RemoveAll(filepath.Join(s.dir, folder))
		return Site{}, err
	}
	if found {
		s.removeFolder(old.folder)
	}
	site, _, err := s.Get(ctx, path)
	return site, err
}

// SetVisibility makes the Site at path Public or Private.
func (s *Sites) SetVisibility(ctx context.Context, path string, v Visibility) error {
	return s.update(ctx, `UPDATE sites SET visibility = ? WHERE path = ?`, v, path)
}

// SetArchived takes the Site at path offline for everyone, or puts it back.
func (s *Sites) SetArchived(ctx context.Context, path string, archived bool) error {
	return s.update(ctx, `UPDATE sites SET archived = ? WHERE path = ?`, archived, path)
}

// Delete removes the Site at path and its files for ever. The path is then
// free. It removes the database row first: if the server stops before the
// folder is gone, Cleanup removes the folder at the next start.
func (s *Sites) Delete(ctx context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	site, ok, err := s.Get(ctx, path)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, site.ID); err != nil {
		return err
	}
	s.removeFolder(site.folder)
	return nil
}

func (s *Sites) update(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

var ErrNotFound = errors.New("no Site at this path")

// Open opens a file of the Site. name uses "/" and has no leading "/". The
// file can never be outside the folder of the Site.
func (s *Sites) Open(site Site, name string) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Join(s.dir, site.folder))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(filepath.FromSlash(name))
}

// Cleanup deletes the folders that no Site uses: an upload that stopped
// before the end, or a delete that stopped between its two steps. Run it at
// start-up, before any upload can be in progress.
func (s *Sites) Cleanup(ctx context.Context) error {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	all, err := s.List(ctx)
	if err != nil {
		return err
	}
	used := map[string]bool{}
	for _, site := range all {
		used[site.folder] = true
	}
	for _, e := range entries {
		if !used[e.Name()] {
			log.Printf("sites: cleanup: remove %s", e.Name())
			s.removeFolder(e.Name())
		}
	}
	return nil
}

func (s *Sites) removeFolder(folder string) {
	if err := os.RemoveAll(filepath.Join(s.dir, folder)); err != nil {
		log.Printf("sites: remove %s: %v", folder, err)
	}
}

// unpack writes the files of a .tar.gz into dir. It refuses an entry that
// can write outside dir (a link, "..", or an absolute path), a dotfile, and
// an upload over the limits. For a one-file Site, it gives the file name.
func (s *Sites) unpack(dir string, kind Kind, tarGz io.Reader) (string, error) {
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()

	gz, err := gzip.NewReader(&limitReader{r: tarGz, n: s.MaxUpload, msg: "the upload is too large"})
	if err != nil {
		if errors.Is(err, ErrBadUpload) {
			return "", err
		}
		return "", badUpload("not a .tar.gz")
	}
	// A small upload can unpack to a very large stream. Limit all of it: the
	// files, the tar headers and padding, and the data after the tar end.
	unpackedStream := &limitReader{
		r: gz, n: s.MaxUnpacked + int64(s.MaxFiles+1)*tarOverhead,
		msg: fmt.Sprintf("more than %d MB after unpacking", s.MaxUnpacked>>20),
	}
	tr := tar.NewReader(unpackedStream)
	var names []string
	var unpacked int64
	// dirs holds each folder that the upload makes, also the parents that a
	// file name like "a/b/c.txt" makes. Folders use disk space, so they
	// count toward MaxFiles.
	dirs := map[string]bool{}
	tooMany := badUpload("more than %d files and folders", s.MaxFiles)
	addDirs := func(dir string) error {
		// Each "/" makes one more folder. Check the depth first, so that a
		// very deep name fails fast.
		if dir != "." && len(names)+strings.Count(dir, "/")+1 > s.MaxFiles {
			return tooMany
		}
		// The name is clean, so each parent is the part before a "/". When a
		// folder is in dirs, its parents are in dirs too.
		for dir != "." && !dirs[dir] {
			dirs[dir] = true
			i := strings.LastIndexByte(dir, '/')
			if i < 0 {
				break
			}
			dir = dir[:i]
		}
		if len(names)+len(dirs) > s.MaxFiles {
			return tooMany
		}
		return nil
	}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, ErrBadUpload) {
				return "", err
			}
			return "", badUpload("broken .tar.gz: %v", err)
		}

		name := strings.TrimSuffix(h.Name, "/")
		if err := checkName(name); err != nil {
			return "", err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if kind == File {
				return "", badUpload("a one-file Site has no folders")
			}
			if err := addDirs(name); err != nil {
				return "", err
			}
			if err := root.MkdirAll(filepath.FromSlash(name), 0o755); err != nil {
				return "", err
			}
			continue
		case tar.TypeReg:
		default:
			return "", badUpload("%q is not a plain file or folder", h.Name)
		}

		names = append(names, name)
		if err := addDirs(pathpkg.Dir(name)); err != nil {
			return "", err
		}
		// Compare with what is left, not the sum: a huge size makes the sum
		// overflow and go negative.
		if h.Size < 0 || h.Size > s.MaxUnpacked-unpacked {
			return "", badUpload("more than %d MB after unpacking", s.MaxUnpacked>>20)
		}
		unpacked += h.Size
		if err := writeFile(root, name, tr); err != nil {
			if errors.Is(err, ErrBadUpload) {
				return "", err
			}
			return "", badUpload("cannot write %q: %v", name, err)
		}
	}

	// The tar end is not the stream end. Read the rest, so that the gzip
	// checksum is checked and data after the end counts toward the limits.
	if _, err := io.Copy(io.Discard, unpackedStream); err != nil {
		if errors.Is(err, ErrBadUpload) {
			return "", err
		}
		return "", badUpload("broken .tar.gz: %v", err)
	}

	switch {
	case kind == File && (len(names) != 1 || strings.Contains(names[0], "/")):
		return "", badUpload("a one-file Site has exactly one file")
	case kind == File:
		return names[0], nil
	case !slices.Contains(names, "index.html"):
		return "", badUpload("a folder needs index.html at its top")
	}
	return "", nil
}

// checkName refuses names that can leave the folder of the Site, and
// dotfiles like .env and .git, which must never go online.
func checkName(name string) error {
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, `\`) || !filepath.IsLocal(filepath.FromSlash(name)) {
		return badUpload("bad file name %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return badUpload("dotfiles are not allowed: %q", name)
		}
	}
	return nil
}

func writeFile(root *os.Root, name string, r io.Reader) error {
	p := filepath.FromSlash(name)
	if err := root.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := root.Create(p)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
