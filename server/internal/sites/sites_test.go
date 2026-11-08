package sites

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kelpikz/shelf/server/internal/store"
)

func newSites(t *testing.T) (*Sites, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db, dir), dir
}

type entry struct {
	name string
	body string
	typ  byte // tar.TypeReg if 0
	link string
	pax  map[string]string
}

func tarGz(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: e.typ, Linkname: e.link, PAXRecords: e.pax}
		if e.typ == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	return &buf
}

func TestCheckPath(t *testing.T) {
	s, _ := newSites(t)
	ctx := context.Background()
	if _, err := s.Publish(ctx, "taken", Folder, tarGz(t, entry{name: "index.html", body: "x"})); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path    string
		taken   bool
		wantErr error
	}{
		{"notes", false, nil},
		{"a", false, nil},
		{"my-cv-2026", false, nil},
		{strings.Repeat("a", 64), false, nil},
		{"taken", true, nil},
		{strings.Repeat("a", 65), false, ErrBadPath},
		{"", false, ErrBadPath},
		{"Notes", false, ErrBadPath},
		{"-notes", false, ErrBadPath},
		{"my_notes", false, ErrBadPath},
		{"notes.html", false, ErrBadPath},
		{"a/b", false, ErrBadPath},
		{"café", false, ErrBadPath},
		{"admin", false, ErrReserved},
		{"api", false, ErrReserved},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			_, taken, err := s.Check(ctx, tt.path)
			if !errors.Is(err, tt.wantErr) || taken != tt.taken {
				t.Errorf("Check(%q) = taken %v, err %v; want taken %v, err %v", tt.path, taken, err, tt.taken, tt.wantErr)
			}
		})
	}
}

func TestPublishRefusesBadPath(t *testing.T) {
	s, _ := newSites(t)
	for _, path := range []string{"Bad", "admin", "../x"} {
		_, err := s.Publish(context.Background(), path, Folder, tarGz(t, entry{name: "index.html", body: "x"}))
		if err == nil {
			t.Errorf("Publish(%q) worked", path)
		}
	}
}

// An upload must never write outside its folder, and a refused upload makes
// no Site.
func TestPublishRefusesUnsafeUploads(t *testing.T) {
	index := entry{name: "index.html", body: "x"}
	tests := []struct {
		name    string
		kind    Kind
		entries []entry
	}{
		{"parent dir", Folder, []entry{index, {name: "../escape.txt", body: "x"}}},
		{"parent dir inside", Folder, []entry{index, {name: "a/../../escape.txt", body: "x"}}},
		{"absolute", Folder, []entry{index, {name: "/escape.txt", body: "x"}}},
		{"backslash", Folder, []entry{index, {name: `..\escape.txt`, body: "x"}}},
		{"symlink", Folder, []entry{index, {name: "link", typ: tar.TypeSymlink, link: "../../shelf.db"}}},
		{"hard link", Folder, []entry{index, {name: "link", typ: tar.TypeLink, link: "index.html"}}},
		{"device", Folder, []entry{index, {name: "dev", typ: tar.TypeChar}}},
		{"dotfile", Folder, []entry{index, {name: ".env", body: "SECRET=1"}}},
		{"dot folder", Folder, []entry{index, {name: ".git/config", body: "x"}}},
		{"no index.html", Folder, []entry{{name: "about.html", body: "x"}}},
		{"index.html not at the top", Folder, []entry{{name: "site/index.html", body: "x"}}},
		{"one-file Site with two files", File, []entry{{name: "a.pdf", body: "x"}, {name: "b.pdf", body: "x"}}},
		{"one-file Site in a folder", File, []entry{{name: "docs/a.pdf", body: "x"}}},
		{"empty", File, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := newSites(t)
			_, err := s.Publish(context.Background(), "notes", tt.kind, tarGz(t, tt.entries...))
			if !errors.Is(err, ErrBadUpload) {
				t.Errorf("got %v, want ErrBadUpload", err)
			}
			if _, ok, _ := s.Get(context.Background(), "notes"); ok {
				t.Error("a refused upload made a Site")
			}
			for _, p := range []string{filepath.Join(dir, "escape.txt"), filepath.Join(dir, "sites", "escape.txt")} {
				if _, err := os.Stat(p); err == nil {
					t.Errorf("the upload wrote %s", p)
				}
			}
		})
	}
}

func TestPublishLimits(t *testing.T) {
	ctx := context.Background()
	index := entry{name: "index.html", body: "x"}

	s, _ := newSites(t)
	s.MaxFiles = 2
	if _, err := s.Publish(ctx, "notes", Folder, tarGz(t, index, entry{name: "a", body: "x"}, entry{name: "b", body: "x"})); !errors.Is(err, ErrBadUpload) {
		t.Errorf("too many files: got %v", err)
	}

	s, _ = newSites(t)
	s.MaxUnpacked = 10
	big := entry{name: "big.txt", body: strings.Repeat("a", 11)}
	if _, err := s.Publish(ctx, "notes", Folder, tarGz(t, index, big)); !errors.Is(err, ErrBadUpload) {
		t.Errorf("too large unpacked: got %v", err)
	}

	s, _ = newSites(t)
	s.MaxUpload = 20 // compressed bytes; the gzip header and end alone are 18
	if _, err := s.Publish(ctx, "notes", Folder, tarGz(t, index)); !errors.Is(err, ErrBadUpload) {
		t.Errorf("too large upload: got %v", err)
	}
}

func TestCleanupRemovesUnusedFolders(t *testing.T) {
	s, dir := newSites(t)
	ctx := context.Background()
	site, err := s.Publish(ctx, "notes", Folder, tarGz(t, entry{name: "index.html", body: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "sites", "notes-half-done")
	os.MkdirAll(stray, 0o755)

	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); err == nil {
		t.Error("the unused folder is still there")
	}
	f, err := s.Open(site, "index.html")
	if err != nil {
		t.Fatalf("the Site lost its files: %v", err)
	}
	f.Close()
}

// The whole stream must be checked, not only the part up to the tar end.
func TestPublishChecksTheWholeStream(t *testing.T) {
	ctx := context.Background()
	index := entry{name: "index.html", body: "x"}

	s, _ := newSites(t)
	bad := tarGz(t, index).Bytes()
	bad[len(bad)-8] ^= 0xff // the gzip checksum is in the last 8 bytes
	if _, err := s.Publish(ctx, "notes", Folder, bytes.NewReader(bad)); !errors.Is(err, ErrBadUpload) {
		t.Errorf("bad checksum: got %v", err)
	}

	s, _ = newSites(t)
	good := tarGz(t, index).Bytes()
	s.MaxUpload = int64(len(good)) + 100
	tail := append(good, bytes.Repeat([]byte{0}, 1000)...)
	if _, err := s.Publish(ctx, "notes", Folder, bytes.NewReader(tail)); !errors.Is(err, ErrBadUpload) {
		t.Errorf("data after the end: got %v", err)
	}
}

// Folders use disk space too, so they count toward MaxFiles.
func TestPublishCountsFolders(t *testing.T) {
	ctx := context.Background()
	index := entry{name: "index.html", body: "x"}

	s, _ := newSites(t)
	s.MaxFiles = 3
	dirs := []entry{index, {name: "a/", typ: tar.TypeDir}, {name: "b/", typ: tar.TypeDir}, {name: "c/", typ: tar.TypeDir}}
	if _, err := s.Publish(ctx, "notes", Folder, tarGz(t, dirs...)); !errors.Is(err, ErrBadUpload) {
		t.Errorf("folder entries: got %v", err)
	}

	s, _ = newSites(t)
	s.MaxFiles = 3
	deep := entry{name: "a/b/c/d/e.txt", body: "x"}
	if _, err := s.Publish(ctx, "notes", Folder, tarGz(t, index, deep)); !errors.Is(err, ErrBadUpload) {
		t.Errorf("folders from a deep file name: got %v", err)
	}

	s, _ = newSites(t)
	s.MaxFiles = 3
	ok := []entry{index, {name: "css/", typ: tar.TypeDir}, {name: "css/a.css", body: "x"}}
	if _, err := s.Publish(ctx, "notes", Folder, tarGz(t, ok...)); err != nil {
		t.Errorf("3 entries with a limit of 3: got %v", err)
	}
}

// A huge size in a tar header must not overflow the sum and skip the limit.
func TestPublishRefusesHugeFileSize(t *testing.T) {
	s, _ := newSites(t)
	s.MaxUnpacked = 16
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.WriteHeader(&tar.Header{Name: "big.bin", Mode: 0o644, Size: math.MaxInt64, Typeflag: tar.TypeReg})
	tw.Write(make([]byte, 1<<20))
	gz.Close()

	dir := filepath.Join(t.TempDir(), "site")
	if _, err := s.unpack(context.Background(), dir, Folder, &buf); !errors.Is(err, ErrBadUpload) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "big.bin")); err == nil {
		t.Error("the upload wrote big.bin")
	}
}

// Data after the tar end is unpacked too, so it counts toward MaxUnpacked.
func TestPublishLimitsDataAfterTarEnd(t *testing.T) {
	s, _ := newSites(t)
	s.MaxUnpacked, s.MaxFiles = 16, 10
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Write(make([]byte, 1<<20))
	gz.Close()

	if _, err := s.Publish(context.Background(), "notes", Folder, &buf); !errors.Is(err, ErrBadUpload) {
		t.Errorf("got %v", err)
	}
}

// A very deep name must fail fast. The old code took seconds on this name.
func TestPublishRefusesDeepNameFast(t *testing.T) {
	s, _ := newSites(t)
	deep := entry{name: strings.Repeat("a/", 40_000) + "x", body: "x"}
	start := time.Now()
	_, err := s.Publish(context.Background(), "notes", Folder, tarGz(t, entry{name: "index.html", body: "x"}, deep))
	if !errors.Is(err, ErrBadUpload) {
		t.Errorf("got %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s", d)
	}
}
