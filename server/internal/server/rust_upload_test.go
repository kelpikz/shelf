package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kelpikz/shelf/server/internal/sites"
)

// Build the CLI first, then set SHELF_CLI_TEST_BIN to its absolute path.
// This checks the real Rust archive and JSON client against the Go API.
func TestRustCLIUpload(t *testing.T) {
	binary := os.Getenv("SHELF_CLI_TEST_BIN")
	if binary == "" {
		t.Skip("set SHELF_CLI_TEST_BIN to the built shelf binary")
	}
	ts := newServer(t)
	key := ts.apiKey(t, "Rust upload test")
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "files-admin.example.com"
		ts.h.ServeHTTP(w, r)
	}))
	defer fixture.Close()
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("Rust file"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(source string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "upload", source, "rust-upload", "--yes")
		cmd.Env = append(os.Environ(), "SHELF_URL="+fixture.URL, "SHELF_KEY="+key,
			"NO_COLOR=1", "NO_PROXY=*", "no_proxy=*", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Rust CLI: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "Uploaded "+filesURL+"/rust-upload") {
			t.Fatalf("missing success: %s", out)
		}
	}
	run(file)
	first, found, err := ts.sites.Get(context.Background(), "rust-upload")
	if err != nil || !found || first.Kind != sites.File || first.Visibility != sites.Private {
		t.Fatalf("new Site: %+v %v", first, err)
	}
	f, err := ts.sites.Open(first, "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := ts.sites.SetVisibility(context.Background(), "rust-upload", sites.Public); err != nil {
		t.Fatal(err)
	}
	if err := ts.sites.SetArchived(context.Background(), "rust-upload", true); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, "folder")
	if err := os.MkdirAll(filepath.Join(folder, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "index.html"), []byte("<link href='css/site.css' rel='stylesheet'><h1>Rust folder</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "css", "site.css"), []byte("h1 {color: green}"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(folder)
	current, _, err := ts.sites.Get(context.Background(), "rust-upload")
	if err != nil || current.Kind != sites.Folder || current.Visibility != sites.Public || !current.Archived {
		t.Fatalf("replacement: %+v %v", current, err)
	}
	if current.Version() == first.Version() {
		t.Fatal("archive was not replaced")
	}
	if f, err := ts.sites.Open(current, "notes.txt"); err == nil {
		f.Close()
		t.Fatal("old file remains")
	}
	if err := ts.sites.SetArchived(context.Background(), "rust-upload", false); err != nil {
		t.Fatal(err)
	}
	if got := body(ts.get(filesURL + "/rust-upload/css/site.css")); got != "h1 {color: green}" {
		t.Fatalf("folder asset: %q", got)
	}
}
