package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kelpikz/shelf/server/internal/store"
)

func newOAuthTest(t *testing.T) *OAuth {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	o := NewOAuth(db)
	now := time.Now()
	o.Now = func() time.Time { return now }
	return o
}

func oauthRequest() (OAuthRequest, string) {
	v := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(v))
	return OAuthRequest{ClientID: "local", RedirectURI: "http://localhost:8091/callback", Resource: "http://localhost:8081/mcp", Scope: "shelf:read", State: "state", Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, v
}

func issueOAuth(t *testing.T, o *OAuth) (OAuthRequest, OAuthTokens) {
	t.Helper()
	r, v := oauthRequest()
	ctx := context.Background()
	pending, err := o.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := o.Approve(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := o.Exchange(ctx, code, r.ClientID, r.RedirectURI, r.Resource, v)
	if err != nil {
		t.Fatal(err)
	}
	return r, tokens
}

func TestOAuthCodeIsBoundToPKCEClientRedirectAndResource(t *testing.T) {
	o := newOAuthTest(t)
	ctx := context.Background()
	r, verifier := oauthRequest()
	for _, field := range []string{"verifier", "client", "redirect", "resource"} {
		t.Run(field, func(t *testing.T) {
			pending, _ := o.Start(ctx, r)
			code, _, err := o.Approve(ctx, pending)
			if err != nil {
				t.Fatal(err)
			}
			client, redirect, resource, v := r.ClientID, r.RedirectURI, r.Resource, verifier
			switch field {
			case "verifier":
				v = strings.Repeat("x", 43)
			case "client":
				client = "other"
			case "redirect":
				redirect += "/other"
			case "resource":
				resource += "/other"
			}
			if _, err := o.Exchange(ctx, code, client, redirect, resource, v); !errors.Is(err, ErrOAuthGrant) {
				t.Fatalf("accepted wrong %s: %v", field, err)
			}
			if _, err := o.Exchange(ctx, code, r.ClientID, r.RedirectURI, r.Resource, verifier); err != nil {
				t.Fatal(err)
			}
			if _, err := o.Exchange(ctx, code, r.ClientID, r.RedirectURI, r.Resource, verifier); !errors.Is(err, ErrOAuthGrant) {
				t.Fatal("code replay accepted")
			}
		})
	}
}

func TestOAuthRefreshRotationReplayAndRevocation(t *testing.T) {
	o := newOAuthTest(t)
	r, tokens := issueOAuth(t, o)
	ctx := context.Background()
	if _, err := o.Check(ctx, tokens.AccessToken, r.Resource); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Check(ctx, tokens.AccessToken, "other"); !errors.Is(err, ErrOAuthGrant) {
		t.Fatal("wrong audience accepted")
	}
	next, err := o.Refresh(ctx, tokens.RefreshToken, r.ClientID, r.Resource)
	if err != nil {
		t.Fatal(err)
	}
	if next.RefreshToken == tokens.RefreshToken {
		t.Fatal("refresh token did not rotate")
	}
	if _, err := o.Refresh(ctx, tokens.RefreshToken, r.ClientID, r.Resource); !errors.Is(err, ErrOAuthGrant) {
		t.Fatal("refresh replay accepted")
	}
	if _, err := o.Check(ctx, next.AccessToken, r.Resource); !errors.Is(err, ErrOAuthGrant) {
		t.Fatal("refresh replay did not revoke grant")
	}
	_, tokens = issueOAuth(t, o)
	if err := o.Revoke(ctx, tokens.RefreshToken, r.ClientID); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Check(ctx, tokens.AccessToken, r.Resource); !errors.Is(err, ErrOAuthGrant) {
		t.Fatal("revoked grant still works")
	}
}

func TestOAuthExpiryAndNoPlaintextCredentials(t *testing.T) {
	o := newOAuthTest(t)
	r, tokens := issueOAuth(t, o)
	var count int
	if err := o.db.QueryRow("SELECT count(*) FROM oauth_tokens WHERE token_hash IN (?,?)", tokens.AccessToken, tokens.RefreshToken).Scan(&count); err != nil || count != 0 {
		t.Fatalf("plaintext tokens stored: %d %v", count, err)
	}
	now := o.Now().Add(o.AccessTTL)
	o.Now = func() time.Time { return now }
	if _, err := o.Check(context.Background(), tokens.AccessToken, r.Resource); !errors.Is(err, ErrOAuthGrant) {
		t.Fatal("expired access token accepted")
	}
	next, err := o.Refresh(context.Background(), tokens.RefreshToken, r.ClientID, r.Resource)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(o.GrantTTL)
	if _, err := o.Refresh(context.Background(), next.RefreshToken, r.ClientID, r.Resource); !errors.Is(err, ErrOAuthGrant) {
		t.Fatal("expired grant accepted")
	}
}

func TestOAuthRequestAndCodeExpiry(t *testing.T) {
	o := newOAuthTest(t)
	ctx := context.Background()
	r, verifier := oauthRequest()
	id, err := o.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	now := o.Now().Add(10 * time.Minute)
	o.Now = func() time.Time { return now }
	if _, err := o.Pending(ctx, id); !errors.Is(err, ErrOAuthGrant) {
		t.Fatal("expired pending request accepted")
	}
	id, err = o.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := o.Approve(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := o.Exchange(ctx, code, r.ClientID, r.RedirectURI, r.Resource, verifier); !errors.Is(err, ErrOAuthGrant) {
		t.Fatal("expired code accepted")
	}
}
