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
//
// The match ignores ASCII case only. Unicode case folding would let, for
// example, the Kelvin sign match "k", so non-ASCII addresses never match.
func IsOwner(id Identity, ownerEmail string) bool {
	if !isASCII(ownerEmail) {
		return false
	}
	for _, e := range id.Emails {
		if e.Verified && isASCII(e.Address) && strings.ToLower(e.Address) == strings.ToLower(ownerEmail) {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// IsPageVisit is true when the person opens the page in the browser tab. It
// is false for requests that a Site on the same site makes, for example an
// <img> or an <iframe> that points at the admin host. Browsers without
// Sec-Fetch headers count as a page visit.
func IsPageVisit(r *http.Request) bool {
	h := r.Header
	if h.Get("Sec-Fetch-Site") == "" && h.Get("Sec-Fetch-Mode") == "" {
		return true
	}
	return h.Get("Sec-Fetch-Mode") == "navigate" &&
		h.Get("Sec-Fetch-Dest") == "document" &&
		h.Get("Sec-Fetch-Site") != "same-site"
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
