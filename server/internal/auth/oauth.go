package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

var ErrOAuthGrant = errors.New("invalid or expired OAuth grant")

type OAuthRequest struct {
	ClientID, RedirectURI, Resource, Scope, State, Challenge string
}

type OAuthTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

type OAuthSession struct {
	Scope     string `json:"scope"`
	Resource  string `json:"aud"`
	ExpiresAt int64  `json:"expires_at"`
}

// OAuth stores only hashes of codes, tokens, and pending request handles.
// Browser handlers must verify the Owner and ask for approval before Approve.
type OAuth struct {
	db                  *sql.DB
	mu                  sync.Mutex
	Now                 func() time.Time
	AccessTTL, GrantTTL time.Duration
}

func NewOAuth(db *sql.DB) *OAuth {
	return &OAuth{db: db, Now: time.Now, AccessTTL: 15 * time.Minute, GrantTTL: 30 * 24 * time.Hour}
}

func (o *OAuth) Start(ctx context.Context, r OAuthRequest) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	id := RandomToken()
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// Expired requests are never usable. Bound accumulation from repeated starts.
	for _, table := range []string{"oauth_requests", "oauth_codes", "oauth_grants"} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE expires_at <= ?", o.Now().Unix()); err != nil {
			return "", err
		}
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM oauth_requests").Scan(&count); err != nil {
		return "", err
	}
	if count >= 100 {
		return "", errors.New("too many pending OAuth requests")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_requests VALUES(?,?,?,?,?,?,?,?)`, hash(id), r.ClientID, r.RedirectURI, r.Resource, r.Scope, r.State, r.Challenge, o.Now().Add(10*time.Minute).Unix())
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (o *OAuth) Pending(ctx context.Context, id string) (OAuthRequest, error) {
	var r OAuthRequest
	err := o.db.QueryRowContext(ctx, `SELECT client_id,redirect_uri,resource,scope,state,challenge FROM oauth_requests WHERE request_hash=? AND expires_at>?`, hash(id), o.Now().Unix()).Scan(&r.ClientID, &r.RedirectURI, &r.Resource, &r.Scope, &r.State, &r.Challenge)
	return r, grantError(err)
}

func (o *OAuth) Approve(ctx context.Context, id string) (string, OAuthRequest, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return "", OAuthRequest{}, err
	}
	defer tx.Rollback()
	var r OAuthRequest
	err = tx.QueryRowContext(ctx, `DELETE FROM oauth_requests WHERE request_hash=? AND expires_at>? RETURNING client_id,redirect_uri,resource,scope,state,challenge`, hash(id), o.Now().Unix()).Scan(&r.ClientID, &r.RedirectURI, &r.Resource, &r.Scope, &r.State, &r.Challenge)
	if err != nil {
		return "", r, grantError(err)
	}
	code := RandomToken()
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_codes VALUES(?,?,?,?,?,?,?)`, hash(code), r.ClientID, r.RedirectURI, r.Resource, r.Scope, r.Challenge, o.Now().Add(time.Minute).Unix())
	if err != nil {
		return "", r, err
	}
	return code, r, tx.Commit()
}

func (o *OAuth) Deny(ctx context.Context, id string) error {
	_, err := o.db.ExecContext(ctx, "DELETE FROM oauth_requests WHERE request_hash=?", hash(id))
	return err
}

func (o *OAuth) Exchange(ctx context.Context, code, client, redirect, resource, verifier string) (OAuthTokens, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthTokens{}, err
	}
	defer tx.Rollback()
	var r OAuthRequest
	err = tx.QueryRowContext(ctx, `DELETE FROM oauth_codes WHERE code_hash=? AND expires_at>? RETURNING client_id,redirect_uri,resource,scope,challenge`, hash(code), o.Now().Unix()).Scan(&r.ClientID, &r.RedirectURI, &r.Resource, &r.Scope, &r.Challenge)
	if err != nil {
		return OAuthTokens{}, grantError(err)
	}
	sum := sha256.Sum256([]byte(verifier))
	if r.ClientID != client || r.RedirectURI != redirect || r.Resource != resource || !Equal(r.Challenge, base64.RawURLEncoding.EncodeToString(sum[:])) {
		return OAuthTokens{}, ErrOAuthGrant
	}
	id := RandomToken()
	expires := o.Now().Add(o.GrantTTL).Unix()
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_grants(id,client_id,resource,scope,expires_at) VALUES(?,?,?,?,?)`, id, client, resource, r.Scope, expires)
	if err != nil {
		return OAuthTokens{}, err
	}
	tokens, err := o.issue(ctx, tx, id, r.Scope, expires)
	if err != nil {
		return OAuthTokens{}, err
	}
	return tokens, tx.Commit()
}

func (o *OAuth) issue(ctx context.Context, tx *sql.Tx, id, scope string, grantExpiry int64) (OAuthTokens, error) {
	now := o.Now().Unix()
	expires := min(o.Now().Add(o.AccessTTL).Unix(), grantExpiry)
	t := OAuthTokens{AccessToken: "shelf_mcp_" + RandomToken(), RefreshToken: "shelf_refresh_" + RandomToken(), TokenType: "Bearer", ExpiresIn: expires - now, Scope: scope}
	_, err := tx.ExecContext(ctx, `INSERT INTO oauth_tokens(token_hash,grant_id,kind,expires_at) VALUES(?,?,'access',?),(?,?,'refresh',?)`, hash(t.AccessToken), id, expires, hash(t.RefreshToken), id, grantExpiry)
	return t, err
}

func (o *OAuth) Refresh(ctx context.Context, token, client, resource string) (OAuthTokens, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthTokens{}, err
	}
	defer tx.Rollback()
	var id, scope string
	var expiry int64
	var used int
	err = tx.QueryRowContext(ctx, `SELECT g.id,g.scope,g.expires_at,t.used FROM oauth_tokens t JOIN oauth_grants g ON g.id=t.grant_id WHERE t.token_hash=? AND t.kind='refresh' AND g.client_id=? AND g.resource=? AND g.revoked=0 AND t.expires_at>? AND g.expires_at>?`, hash(token), client, resource, o.Now().Unix(), o.Now().Unix()).Scan(&id, &scope, &expiry, &used)
	if err != nil {
		return OAuthTokens{}, grantError(err)
	}
	if used != 0 {
		if _, err = tx.ExecContext(ctx, "UPDATE oauth_grants SET revoked=1 WHERE id=?", id); err != nil {
			return OAuthTokens{}, err
		}
		if err = tx.Commit(); err != nil {
			return OAuthTokens{}, err
		}
		return OAuthTokens{}, ErrOAuthGrant
	}
	result, err := tx.ExecContext(ctx, "UPDATE oauth_tokens SET used=1 WHERE token_hash=? AND used=0", hash(token))
	if err != nil {
		return OAuthTokens{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return OAuthTokens{}, err
	}
	if n != 1 {
		return OAuthTokens{}, ErrOAuthGrant
	}
	tokens, err := o.issue(ctx, tx, id, scope, expiry)
	if err != nil {
		return OAuthTokens{}, err
	}
	return tokens, tx.Commit()
}

func (o *OAuth) Check(ctx context.Context, token, resource string) (OAuthSession, error) {
	var s OAuthSession
	err := o.db.QueryRowContext(ctx, `SELECT g.scope,g.resource,t.expires_at FROM oauth_tokens t JOIN oauth_grants g ON g.id=t.grant_id WHERE t.token_hash=? AND t.kind='access' AND t.used=0 AND t.expires_at>? AND g.expires_at>? AND g.revoked=0 AND g.resource=?`, hash(token), o.Now().Unix(), o.Now().Unix(), resource).Scan(&s.Scope, &s.Resource, &s.ExpiresAt)
	return s, grantError(err)
}

func (o *OAuth) Revoke(ctx context.Context, token, client string) error {
	_, err := o.db.ExecContext(ctx, `UPDATE oauth_grants SET revoked=1 WHERE client_id=? AND id IN (SELECT grant_id FROM oauth_tokens WHERE token_hash=?)`, client, hash(token))
	return err
}

func grantError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOAuthGrant
	}
	return err
}
