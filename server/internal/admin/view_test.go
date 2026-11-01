package admin

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestViewResumesAfterGitHubLogin(t *testing.T) {
	h := newApp(t, ownerID)
	state := strings.Repeat("a", 43)
	target := "/view?" + url.Values{"state": {state}, "return": {"/draft/nested.html?x=1"}}.Encode()
	req := httptest.NewRequest("GET", target, nil)
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	resp := do(h, req)
	next := cookie(resp, viewNextCookie)
	if next == nil || resp.Header.Get("Location") != "/login" {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	start := do(h, httptest.NewRequest("GET", "/auth/github", nil))
	loc, _ := url.Parse(start.Header.Get("Location"))
	callback := httptest.NewRequest("GET", "/auth/github/callback?code=abc&state="+url.QueryEscape(loc.Query().Get("state")), nil)
	callback.AddCookie(cookie(start, loginCookie))
	callback.AddCookie(next)
	finished := do(h, callback)
	if finished.Header.Get("Location") != target {
		t.Fatalf("lost continuation: %s", finished.Header.Get("Location"))
	}
	view := httptest.NewRequest("GET", finished.Header.Get("Location"), nil)
	view.AddCookie(cookie(finished, sessionCookie))
	issued := do(h, view)
	u, err := url.Parse(issued.Header.Get("Location"))
	if err != nil || u.Host != "files.example.com" || u.Path != "/_shelf/view" || u.Query().Get("code") == "" || u.Query().Get("return") != "/draft/nested.html?x=1" {
		t.Fatalf("handoff: %v %v", u, err)
	}
	if strings.Contains(u.String(), cookie(finished, sessionCookie).Value) {
		t.Fatal("admin token in URL")
	}
	for _, path := range []string{target, "/login"} {
		sub := httptest.NewRequest("GET", path, nil)
		sub.AddCookie(cookie(finished, sessionCookie))
		sub.AddCookie(next)
		sub.Header.Set("Sec-Fetch-Site", "same-site")
		sub.Header.Set("Sec-Fetch-Mode", "no-cors")
		sub.Header.Set("Sec-Fetch-Dest", "image")
		result := do(h, sub)
		if path == target && result.StatusCode != 403 {
			t.Fatalf("subresource handoff: %d", result.StatusCode)
		}
	}
}
