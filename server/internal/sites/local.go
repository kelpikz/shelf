package sites

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PublishLocal makes the Site at path from a local file or folder. It packs
// the files into a .tar.gz, the same as the CLI will, and calls Publish. The
// "server publish" command uses it until the CLI exists.
func (s *Sites) PublishLocal(ctx context.Context, path, local string) (Site, error) {
	info, err := os.Stat(local)
	if err != nil {
		return Site{}, err
	}
	kind := File
	if info.IsDir() {
		kind = Folder
	}

	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(pack(pw, local, info)) }()
	site, err := s.Publish(ctx, path, kind, pr)
	pr.Close()
	return site, err
}

// skip is true for files that must never go online: dotfiles like .env and
// .git, and node_modules.
func skip(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

func pack(w io.Writer, local string, info fs.FileInfo) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	add := func(name, p string, size int64) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: size, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	}

	var err error
	if info.IsDir() {
		err = filepath.WalkDir(local, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p != local && skip(d.Name()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(local, p)
			return add(filepath.ToSlash(rel), p, fi.Size())
		})
	} else {
		err = add(info.Name(), local, info.Size())
	}
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
