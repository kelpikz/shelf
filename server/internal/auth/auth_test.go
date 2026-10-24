package auth

import (
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
