// Package access holds the login rules: who the Owner is, sessions, and the
// checks that stop other sites from acting as the Owner.
package access

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// IsOwner is true only when the identity has ownerEmail and GitHub marks it
// verified. GitHub lets anyone add an email they do not own, so the verified
// flag is the real check.
func IsOwner(id Identity, ownerEmail string) bool {
	for _, e := range id.Emails {
		if e.Verified && strings.EqualFold(e.Address, ownerEmail) {
			return true
		}
	}
	return false
}

// SameOrigin is true when a state-changing request comes from the admin page
// itself. The browser sets Origin and Sec-Fetch-Site, and scripts cannot
// change them. SameSite cookies do not help here, because the Sites host and
// the admin host are the same site.
func SameOrigin(r *http.Request, adminOrigin string) bool {
	if r.Header.Get("Origin") != adminOrigin {
		return false
	}
	site := r.Header.Get("Sec-Fetch-Site")
	return site == "" || site == "same-origin"
}

// Equal compares two secrets in constant time.
func Equal(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RandomToken gives 32 random bytes as URL-safe text.
func RandomToken() string {
	b := make([]byte, 32)
	rand.Read(b) // never fails since Go 1.24
	return base64.RawURLEncoding.EncodeToString(b)
}
