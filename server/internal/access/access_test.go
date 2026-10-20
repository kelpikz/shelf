package access

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsOwner(t *testing.T) {
	const owner = "owner@example.com"
	tests := []struct {
		name   string
		id     int64
		emails []Email
		want   bool
	}{
		{"verified match", 1, []Email{{"owner@example.com", true}}, true},
		{"case does not matter", 1, []Email{{"Owner@Example.com", true}}, true},
		{"unverified match", 1, []Email{{"owner@example.com", false}}, false},
		{"other email", 1, []Email{{"someone@example.com", true}}, false},
		{"no emails", 1, nil, false},
		{"other GitHub account with the email", 2, []Email{{"owner@example.com", true}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsOwner(Identity{ID: tt.id, Emails: tt.emails}, owner, 1); got != tt.want {
				t.Errorf("IsOwner = %v, want %v", got, tt.want)
			}
		})
	}
}

// Unicode case folding matches the Kelvin sign to "k" and the long s to
// "s". IsOwner must not.
func TestIsOwnerRefusesUnicodeLookalikes(t *testing.T) {
	const owner = "kas@example.com"
	for _, addr := range []string{
		string(rune(0x212A)) + "as@example.com",      // Kelvin sign
		"ka" + string(rune(0x017F)) + "@example.com", // long s
	} {
		if !strings.EqualFold(addr, owner) {
			t.Fatalf("%q: test is wrong, EqualFold does not match it", addr)
		}
		if IsOwner(Identity{ID: 1, Emails: []Email{{addr, true}}}, owner, 1) {
			t.Errorf("%q matched the Owner", addr)
		}
	}
}

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
			if got := IsPageVisit(r); got != tt.want {
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
			if got := SameOrigin(r, admin); got != tt.want {
				t.Errorf("SameOrigin = %v, want %v", got, tt.want)
			}
		})
	}
}
