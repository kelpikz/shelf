package sites

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishLocalSkipsDotfilesAndToolFolders(t *testing.T) {
	s, _ := newSites(t)
	local := t.TempDir()
	for name, body := range map[string]string{
		"index.html":          "notes",
		".env":                "SECRET=1",
		".git/config":         "x",
		"node_modules/a/a.js": "x",
		"img/.DS_Store":       "x",
	} {
		p := filepath.Join(local, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	site, err := s.PublishLocal(context.Background(), "notes", local)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Open(site, "index.html")
	if err != nil {
		t.Fatalf("index.html is missing: %v", err)
	}
	f.Close()
	for _, name := range []string{".env", ".git/config", "node_modules/a/a.js", "img/.DS_Store"} {
		if f, err := s.Open(site, name); err == nil {
			f.Close()
			t.Errorf("%s went online", name)
		}
	}
}

func TestPublishLocalOneFile(t *testing.T) {
	s, _ := newSites(t)
	local := filepath.Join(t.TempDir(), "cv.pdf")
	os.WriteFile(local, []byte("%PDF"), 0o644)

	site, err := s.PublishLocal(context.Background(), "cv", local)
	if err != nil {
		t.Fatal(err)
	}
	if site.Kind != File || site.File != "cv.pdf" {
		t.Errorf("got kind %q, file %q", site.Kind, site.File)
	}
}
