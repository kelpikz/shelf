package config

import "testing"

func TestLoadChecksValues(t *testing.T) {
	tests := []struct {
		name, adminURL, email string
		ok                    bool
	}{
		{"good", "https://files-admin.example.com", "owner@example.com", true},
		{"local dev", "http://localhost:8080", "owner@example.com", true},
		{"trailing slash", "https://files-admin.example.com/", "owner@example.com", true},
		{"path", "https://files-admin.example.com/admin", "owner@example.com", false},
		{"query", "https://files-admin.example.com?x=1", "owner@example.com", false},
		{"empty query", "https://files-admin.example.com?", "owner@example.com", false},
		{"fragment", "https://files-admin.example.com#x", "owner@example.com", false},
		{"user info", "https://me@files-admin.example.com", "owner@example.com", false},
		{"no scheme", "files-admin.example.com", "owner@example.com", false},
		{"non-ASCII email", "https://files-admin.example.com", "owner@exampl" + string(rune(0x212A)) + ".com", false},
		{"not an email", "https://files-admin.example.com", "owner", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OWNER_EMAIL", tt.email)
			t.Setenv("OWNER_GITHUB_ID", "1")
			t.Setenv("GITHUB_CLIENT_ID", "id")
			t.Setenv("GITHUB_CLIENT_SECRET", "secret")
			t.Setenv("ADMIN_URL", tt.adminURL)
			_, err := Load()
			if (err == nil) != tt.ok {
				t.Errorf("Load() error = %v, want ok = %v", err, tt.ok)
			}
		})
	}
}

func TestLoadChecksOwnerGitHubID(t *testing.T) {
	for id, ok := range map[string]bool{"583231": true, "": false, "ajitha": false, "0": false, "-1": false} {
		t.Run(id, func(t *testing.T) {
			t.Setenv("OWNER_EMAIL", "owner@example.com")
			t.Setenv("OWNER_GITHUB_ID", id)
			t.Setenv("GITHUB_CLIENT_ID", "id")
			t.Setenv("GITHUB_CLIENT_SECRET", "secret")
			t.Setenv("ADMIN_URL", "https://files-admin.example.com")
			c, err := Load()
			if (err == nil) != ok {
				t.Fatalf("Load() error = %v, want ok = %v", err, ok)
			}
			if ok && c.OwnerGitHubID != 583231 {
				t.Errorf("OwnerGitHubID = %d", c.OwnerGitHubID)
			}
		})
	}
}
