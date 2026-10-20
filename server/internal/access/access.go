// Package access holds the login rules: who the Owner is, sessions, and the
// checks that stop other sites from acting as the Owner.
package access

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/kelpikz/shelf/server/internal/utils"
)

// IsOwner is true only when the identity is the GitHub account ownerID and it
// has ownerEmail, which GitHub marks verified. GitHub lets anyone add an email
// they do not own, so the verified flag is the real check. The account ID
// never changes, so an email that moves to a different account does not match.
//
// The match ignores ASCII case only. Unicode case folding would let, for
// example, the Kelvin sign match "k", so non-ASCII addresses never match.
func IsOwner(id Identity, ownerEmail string, ownerID int64) bool {
	if id.ID != ownerID || !utils.IsASCII(ownerEmail) {
		return false
	}
	for _, e := range id.Emails {
		if e.Verified && utils.IsASCII(e.Address) && strings.ToLower(e.Address) == strings.ToLower(ownerEmail) {
			return true
		}
	}
	return false
}

// IsPageVisit is true when the person opens the page in the browser tab. It
// is false for requests that a page on the Sites host makes, for example an
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

// SameOrigin is true when a state-changing request comes from the Admin page
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
