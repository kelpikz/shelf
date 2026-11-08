package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPrivateSiteBrowserHandoff(t *testing.T) {
	ts := newServer(t)
	ts.publish(t, "draft", map[string]string{"index.html": "private content", "style.css": "body{}"})
	adminCookie, _ := ts.login(t)
	request := func(target string, cookies ...*http.Cookie) *http.Response {
		r := httptest.NewRequest("GET", target, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		ts.h.ServeHTTP(w, r)
		return w.Result()
	}
	first := request(filesURL + "/_shelf/login?return=%2Fdraft%2F")
	if first.StatusCode != 302 {
		t.Fatalf("start: %d", first.StatusCode)
	}
	if len(first.Cookies()) == 0 {
		t.Fatal("no state cookie")
	}
	state := first.Cookies()[0]
	issued := request(first.Header.Get("Location"), adminCookie)
	if issued.StatusCode != 303 {
		t.Fatalf("issue: %d %s", issued.StatusCode, body(issued))
	}
	callback := issued.Header.Get("Location")
	if resp := request(callback); resp.StatusCode != 403 {
		t.Fatalf("unbound: %d", resp.StatusCode)
	}
	completed := request(callback, state)
	if completed.StatusCode != 303 || completed.Header.Get("Location") != "/draft/" {
		t.Fatalf("callback: %d %s", completed.StatusCode, completed.Header.Get("Location"))
	}
	var view *http.Cookie
	for _, c := range completed.Cookies() {
		if c.Name == "__Host-shelf_view" {
			view = c
		}
	}
	if view == nil || !view.HttpOnly || !view.Secure || view.Domain != "" {
		t.Fatal("bad view cookie")
	}
	if resp := request(callback, state); resp.StatusCode != 403 {
		t.Fatalf("replay: %d", resp.StatusCode)
	}
	for _, path := range []string{"/draft/", "/draft/style.css", "/draft"} {
		resp := request(filesURL+path, view)
		if (resp.StatusCode != 200 && resp.StatusCode != 302) || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("private %s: %d %v", path, resp.StatusCode, resp.Header)
		}
	}
	for _, tc := range []struct {
		site, mode, dest string
		want             int
	}{
		{"same-origin", "no-cors", "style", 200},
		{"same-site", "no-cors", "style", 404},
		{"cross-site", "navigate", "document", 200},
		{"cross-site", "navigate", "iframe", 404},
	} {
		r := httptest.NewRequest("GET", filesURL+"/draft/style.css", nil)
		r.AddCookie(view)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		r.Header.Set("Sec-Fetch-Mode", tc.mode)
		r.Header.Set("Sec-Fetch-Dest", tc.dest)
		w := httptest.NewRecorder()
		ts.h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("metadata %+v: %d", tc, w.Code)
		}
	}
	asset := request(filesURL+"/draft/style.css", view)
	etag := asset.Header.Get("ETag")
	asset.Body.Close()
	for _, owner := range []bool{false, true} {
		r := httptest.NewRequest("GET", filesURL+"/draft/style.css", nil)
		r.Header.Set("If-None-Match", etag)
		if owner {
			r.AddCookie(view)
		}
		w := httptest.NewRecorder()
		ts.h.ServeHTTP(w, r)
		want := 404
		if owner {
			want = 304
		}
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("conditional owner=%v: %d %v", owner, w.Code, w.Header())
		}
	}
	// A view cookie is not an admin login or a CLI key.
	if resp := request(adminURL+"/", view); resp.StatusCode != 303 {
		t.Fatal("view authorized admin")
	}
	if resp := ts.api("PATCH", "/sites/draft", view.Value, map[string]any{"visibility": "public"}); resp.StatusCode != 401 {
		t.Fatal("view authorized API")
	}
	if resp := request(filesURL+"/draft/", adminCookie); resp.StatusCode != 404 {
		t.Fatal("admin cookie authorized files host")
	}
	r := httptest.NewRequest("GET", filesURL+"/draft/style.css", nil)
	r.AddCookie(view)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Sec-Fetch-Mode", "no-cors")
	r.Header.Set("Sec-Fetch-Dest", "style")
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("cross-site subresource: %d", w.Code)
	}
	ts.sites.SetArchived(context.Background(), "draft", true)
	if resp := request(filesURL+"/draft/", view); resp.StatusCode != 404 {
		t.Fatal("Owner can view Archived Site")
	}
	ts.sites.SetArchived(context.Background(), "draft", false)
	ts.sessions.Delete(context.Background(), adminCookie.Value)
	if resp := request(filesURL+"/draft/", view); resp.StatusCode != 404 {
		t.Fatal("view survives logout")
	}
	for _, bad := range []string{"https://evil.example/", "//evil.example/", "/\\evil.example/"} {
		resp := request(filesURL + "/_shelf/login?return=" + url.QueryEscape(bad))
		if resp.StatusCode != 400 {
			t.Fatalf("unsafe return %q: %d", bad, resp.StatusCode)
		}
	}
}
