package sites

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefusedReplacementKeepsOldFiles(t *testing.T) {
	index := entry{name: "index.html", body: "new"}
	for name, entries := range map[string][]entry{
		"duplicate file":               {index, index},
		"file then directory":          {index, {name: "a", body: "x"}, {name: "a/", typ: tar.TypeDir}},
		"directory then file":          {index, {name: "a/", typ: tar.TypeDir}, {name: "a", body: "x"}},
		"file parent":                  {index, {name: "a", body: "x"}, {name: "a/b", body: "x"}},
		"implicit directory then file": {index, {name: "a/b", body: "x"}, {name: "a", body: "x"}},
		"node modules":                 {index, {name: "js/node_modules/a.js", body: "x"}},
		"control character":            {index, {name: "a\nb", body: "x"}},
		"long component":               {index, {name: strings.Repeat("a", 256), body: "x"}},
		"deep path":                    {index, {name: strings.Repeat("a/", 16) + "b", body: "x"}},
		"long path":                    {index, {name: strings.Repeat(strings.Repeat("a", 250)+"/", 5) + "b", body: "x"}},
		"sparse metadata":              {index, {name: "sparse.txt", pax: map[string]string{"SCHILY.realsize": "0"}}},
	} {
		t.Run(name, func(t *testing.T) {
			s, dir := newSites(t)
			old, err := s.Publish(context.Background(), "notes", Folder, tarGz(t, entry{name: "index.html", body: "old"}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Publish(context.Background(), "notes", Folder, tarGz(t, entries...))
			if !errors.Is(err, ErrBadUpload) {
				t.Fatalf("got %v, want bad upload", err)
			}
			current, _, err := s.Get(context.Background(), "notes")
			if err != nil || current.Version() != old.Version() {
				t.Fatalf("old Site changed: %v", err)
			}
			f, err := s.Open(current, "index.html")
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(f)
			f.Close()
			if err != nil || string(b) != "old" {
				t.Fatalf("old content = %q: %v", b, err)
			}
			folders, err := os.ReadDir(filepath.Join(dir, "sites"))
			if err != nil || len(folders) != 1 {
				t.Fatalf("staging folder remains: %v, %v", folders, err)
			}
		})
	}
}

type cancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *cancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func TestPublishCancellationKeepsOldSite(t *testing.T) {
	s, dir := newSites(t)
	old, err := s.Publish(context.Background(), "notes", File, tarGz(t, entry{name: "a.txt", body: "old"}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	archive := tarGz(t, entry{name: "b.txt", body: strings.Repeat("new", 10000)})
	_, err = s.Publish(ctx, "notes", File, &cancelReader{Reader: bytes.NewReader(archive.Bytes()), cancel: cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
	current, _, _ := s.Get(context.Background(), "notes")
	if current.Version() != old.Version() {
		t.Fatal("cancelled upload replaced the old Site")
	}
	folders, _ := os.ReadDir(filepath.Join(dir, "sites"))
	if len(folders) != 1 {
		t.Fatal("cancelled upload left a folder")
	}
	if _, err := s.Publish(context.Background(), "notes", File, tarGz(t, entry{name: "c.txt", body: "ok"})); err != nil {
		t.Fatalf("slot after cancellation: %v", err)
	}
}

func TestStorageQuotaIncludesOldAndUnusedFolders(t *testing.T) {
	s, dir := newSites(t)
	s.MaxUnpacked = 8
	old, err := s.Publish(context.Background(), "notes", File, tarGz(t, entry{name: "a.txt", body: "12345678"}))
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "sites", "unused")
	if err := os.Mkdir(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "unused.txt"), []byte("123"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.MaxStorage = 18 // 8 old + 3 unused + 8 reserved does not fit.
	if _, err := s.Publish(context.Background(), "notes", File, mustNotRead{}); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("quota: %v", err)
	}
	current, _, _ := s.Get(context.Background(), "notes")
	if current.Version() != old.Version() {
		t.Fatal("quota refusal changed the Site")
	}
	s.MaxStorage = 19
	if _, err := s.Publish(context.Background(), "notes", File, tarGz(t, entry{name: "b.txt", body: "ok"})); err != nil {
		t.Fatalf("exact quota: %v", err)
	}
}

func TestPublishChecksDiskBeforeReading(t *testing.T) {
	for _, reason := range []string{"free space", "quota", "disk error"} {
		t.Run(reason, func(t *testing.T) {
			s, _ := newSites(t)
			s.freeSpace = func(string) (uint64, error) { return 1 << 40, nil }
			if reason == "free space" {
				s.freeSpace = func(string) (uint64, error) { return 0, nil }
			}
			if reason == "quota" {
				s.MaxStorage = 1
			}
			if reason == "disk error" {
				s.freeSpace = func(string) (uint64, error) { return 0, errors.New("disk unavailable") }
			}
			_, err := s.Publish(context.Background(), "notes", File, mustNotRead{})
			if err == nil {
				t.Fatal("disk check passed")
			}
			if reason != "disk error" && !errors.Is(err, ErrStorageFull) {
				t.Fatalf("got %v, want storage refusal", err)
			}
		})
	}
}

type mustNotRead struct{}

func (mustNotRead) Read([]byte) (int, error) { panic("archive read before disk check") }
