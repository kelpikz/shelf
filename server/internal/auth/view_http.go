package auth

import (
	"net/http"
	"net/url"
	"strings"
)

// ViewNavigation allows top-level handoffs between the two hosts. Subresource
// requests must never start or complete a browser login.
func ViewNavigation(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "" && r.Header.Get("Sec-Fetch-Mode") == "" {
		return true
	}
	return r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
}

// ViewReturn accepts only local paths outside the reserved handoff routes.
func ViewReturn(raw string) (string, bool) {
	if raw == "" {
		return "/", true
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(raw, "\\\r\n") || strings.ContainsAny(u.Path, "\\\r\n") || strings.HasPrefix(u.Path, "/_shelf/") {
		return "", false
	}
	return u.RequestURI(), true
}
