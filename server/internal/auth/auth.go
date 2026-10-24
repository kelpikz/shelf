// Package auth identifies the Owner and manages login sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"github.com/kelpikz/shelf/server/internal/utils"
)

// Identity is what a login provider tells us about the person.
type Identity struct {
	ID        int64
	Login     string
	AvatarURL string
	Emails    []Email
}

type Email struct {
	Address  string
	Verified bool
}

// Provider is the seam for a login provider. GitHub is the only one in v1.
// Tests use a fake, and Google can be a second adapter later.
type Provider interface {
	// LoginURL is the address that starts the login. The verifier is the
	// PKCE secret; only its S256 hash goes in the URL.
	LoginURL(state, verifier string) string
	// Finish swaps the code from the callback for the person's identity.
	Finish(ctx context.Context, code, verifier string) (Identity, error)
}

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
