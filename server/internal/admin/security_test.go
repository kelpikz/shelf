package admin

import (
	"net/http/httptest"
	"testing"
)

func TestIsPageVisit(t *testing.T) {
	tests := []struct {
		name, site, mode, dest string
		want                   bool
	}{
		{"old browser without fetch metadata", "", "", "", true},
		{"typed URL or bookmark", "none", "navigate", "document", true},
		{"link on the Admin page", "same-origin", "navigate", "document", true},
		{"back from GitHub", "cross-site", "navigate", "document", true},
		{"img on a Site", "same-site", "no-cors", "image", false},
		{"iframe on a Site", "same-site", "navigate", "iframe", false},
		{"link on a Site", "same-site", "navigate", "document", false},
		{"fetch from the Admin page", "same-origin", "cors", "empty", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			for k, v := range map[string]string{"Sec-Fetch-Site": tt.site, "Sec-Fetch-Mode": tt.mode, "Sec-Fetch-Dest": tt.dest} {
				if v != "" {
					r.Header.Set(k, v)
				}
			}
			if got := isPageVisit(r); got != tt.want {
				t.Errorf("IsPageVisit = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSameOrigin(t *testing.T) {
	const admin = "https://files-admin.example.com"
	tests := []struct {
		name, origin, site string
		want               bool
	}{
		{"Admin page", admin, "same-origin", true},
		{"old browser without fetch metadata", admin, "", true},
		{"sites host", "https://files.example.com", "same-site", false},
		{"right origin, wrong fetch site", admin, "same-site", false},
		{"no origin", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/logout", nil)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if tt.site != "" {
				r.Header.Set("Sec-Fetch-Site", tt.site)
			}
			if got := sameOrigin(r, admin); got != tt.want {
				t.Errorf("SameOrigin = %v, want %v", got, tt.want)
			}
		})
	}
}
