package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kelpikz/shelf/server/internal/sites"
)

func TestUnlistedSiteOpensButIsNotListed(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()
	ts.publish(t, "secret", map[string]string{"index.html": "secret"})
	ts.publish(t, "gone", map[string]string{"index.html": "gone"})
	if err := ts.sites.SetVisibility(ctx, "secret", sites.Unlisted); err != nil {
		t.Fatal(err)
	}
	ts.sites.SetVisibility(ctx, "gone", sites.Unlisted)
	ts.sites.SetArchived(ctx, "gone", true)

	resp := ts.get(filesURL + "/secret/")
	if got := body(resp); resp.StatusCode != http.StatusOK || got != "secret" {
		t.Fatalf("visitor: got %d %q", resp.StatusCode, got)
	}
	if resp.Header.Get("X-Robots-Tag") != "noindex" {
		t.Error("unlisted file has no X-Robots-Tag: noindex")
	}
	if resp := ts.get(filesURL + "/gone/"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("archived unlisted: got %d", resp.StatusCode)
	}
	if page := body(ts.get(filesURL + "/")); strings.Contains(page, "/secret") {
		t.Error("unlisted Site is on the Visitor's Index page")
	}

	r := httptest.NewRequest("GET", filesURL+"/", nil)
	r.AddCookie(ts.viewCookie(t))
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	if page := w.Body.String(); !strings.Contains(page, `href="/secret/"`) || !strings.Contains(page, "unlisted") {
		t.Error("Owner does not see the unlisted Site with its tag")
	}
}
