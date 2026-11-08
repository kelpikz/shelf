package server

import (
	"context"
	"net/http"
	"testing"
)

func TestSiteManagementAPI(t *testing.T) {
	ts := newServer(t)
	key := ts.apiKey(t, "management")
	ts.publish(t, "draft", map[string]string{"index.html": "draft"})
	ts.publish(t, "old", map[string]string{"index.html": "old"})
	ts.sites.SetArchived(context.Background(), "old", true)
	for _, tc := range []struct {
		path  string
		count int
	}{{"/sites", 1}, {"/sites?all=true", 2}} {
		resp := ts.api("GET", tc.path, key, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("list: %d", resp.StatusCode)
		}
		list := decode[[]map[string]any](t, resp)
		if len(list) != tc.count {
			t.Fatalf("list: %+v", list)
		}
	}
	for _, change := range []map[string]any{{"visibility": "public"}, {"visibility": "private"}, {"archived": true}, {"archived": false}} {
		resp := ts.api("PATCH", "/sites/draft", key, change)
		if resp.StatusCode != 200 {
			t.Fatalf("change %+v: %d %s", change, resp.StatusCode, body(resp))
		}
		site := decode[map[string]any](t, resp)
		for name, want := range change {
			if site[name] != want {
				t.Fatalf("change: %+v", site)
			}
		}
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"visibility": "oops"}, map[string]any{"archived": "yes"}, map[string]any{"visibility": "public", "archived": true}, map[string]any{"unknown": true}, nil} {
		if resp := ts.api("PATCH", "/sites/draft", key, bad); resp.StatusCode != 400 {
			t.Fatalf("bad patch %+v: %d", bad, resp.StatusCode)
		}
	}
	if resp := ts.api("PATCH", "/sites/missing", key, map[string]any{"archived": true}); resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		path := "/sites/draft"
		if method == "GET" {
			path = "/sites"
		}
		if resp := ts.api(method, path, "", map[string]any{"archived": true}); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauth %s: %d", method, resp.StatusCode)
		}
	}
	if resp := ts.api("DELETE", "/sites/draft", key, nil); resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	if _, found, _ := ts.sites.Get(context.Background(), "draft"); found {
		t.Fatal("Site remains")
	}
	if resp := ts.api("DELETE", "/sites/draft", key, nil); resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
}
