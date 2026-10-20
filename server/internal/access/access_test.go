package access

import (
	"net/http/httptest"
	"testing"
)

func TestIsOwner(t *testing.T) {
	const owner = "owner@example.com"
	tests := []struct {
		name   string
		emails []Email
		want   bool
	}{
		{"verified match", []Email{{"owner@example.com", true}}, true},
		{"case does not matter", []Email{{"Owner@Example.com", true}}, true},
		{"unverified match", []Email{{"owner@example.com", false}}, false},
		{"other email", []Email{{"someone@example.com", true}}, false},
		{"no emails", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsOwner(Identity{Emails: tt.emails}, owner); got != tt.want {
				t.Errorf("IsOwner = %v, want %v", got, tt.want)
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
		{"admin page", admin, "same-origin", true},
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
			if got := SameOrigin(r, admin); got != tt.want {
				t.Errorf("SameOrigin = %v, want %v", got, tt.want)
			}
		})
	}
}
