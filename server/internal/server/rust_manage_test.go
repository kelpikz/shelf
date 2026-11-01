package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRustCLIManagement(t *testing.T) {
	binary := os.Getenv("SHELF_CLI_TEST_BIN")
	if binary == "" {
		t.Skip("set SHELF_CLI_TEST_BIN")
	}
	ts := newServer(t)
	key := ts.apiKey(t, "management")
	ts.publish(t, "draft", map[string]string{"index.html": "draft"})
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Host = "files-admin.example.com"; ts.h.ServeHTTP(w, r) }))
	defer fixture.Close()
	run := func(input string, args ...string) (string, bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = append(os.Environ(), "SHELF_URL="+fixture.URL, "SHELF_KEY="+key, "NO_COLOR=1", "NO_PROXY=*", "no_proxy=*", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=")
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}
	for _, args := range [][]string{{"list"}, {"public", "draft"}, {"private", "draft"}, {"archive", "draft"}} {
		if out, ok := run("", args...); !ok {
			t.Fatalf("%v: %s", args, out)
		}
	}
	if out, ok := run("", "list"); !ok || strings.Contains(out, "draft") {
		t.Fatalf("active list: %s", out)
	}
	if out, ok := run("", "list", "--all"); !ok || !strings.Contains(out, "draft") || !strings.Contains(out, "archived") {
		t.Fatalf("all list: %s", out)
	}
	if out, ok := run("", "unarchive", "draft"); !ok {
		t.Fatal(out)
	}
	if out, ok := run("", "delete", "draft", "--yes"); ok {
		t.Fatalf("delete accepted --yes: %s", out)
	}
	// Piped input is supported, but only the exact path confirms deletion.
	if out, ok := run("wrong\n", "delete", "draft"); ok && strings.Contains(out, "Deleted") {
		t.Fatal(out)
	}
	if _, found, _ := ts.sites.Get(context.Background(), "draft"); !found {
		t.Fatal("wrong confirmation deleted Site")
	}
	if out, ok := run("draft\n", "delete", "draft"); !ok {
		t.Fatal(out)
	}
	if _, found, _ := ts.sites.Get(context.Background(), "draft"); found {
		t.Fatal("confirmed Site remains")
	}
}
