package access

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"time"
)

// Session is a logged-in Owner on the Admin page.
type Session struct {
	CSRF      string
	GitHubID  int64
	Login     string
	AvatarURL string
	CreatedAt time.Time
	RenewedAt time.Time
	// ExpiresAt is the idle deadline. It is never later than MaxExpiresAt.
	ExpiresAt    time.Time
	MaxExpiresAt time.Time
}

// Sessions keeps sessions in SQLite. The table holds only a hash of each
// session token, so a copy of the database does not log anyone in.
//
// A session ends after IdleTTL without use, or MaxTTL after login, whichever
// comes first. Use moves the idle deadline, at most once each RenewEvery.
type Sessions struct {
	db         *sql.DB
	IdleTTL    time.Duration
	MaxTTL     time.Duration
	RenewEvery time.Duration
	Now        func() time.Time
}

func NewSessions(db *sql.DB) *Sessions {
	return &Sessions{
		db:         db,
		IdleTTL:    30 * 24 * time.Hour,
		MaxTTL:     90 * 24 * time.Hour,
		RenewEvery: 24 * time.Hour,
		Now:        time.Now,
	}
}

// Create makes a session and gives the token for the cookie.
func (s *Sessions) Create(ctx context.Context, id Identity) (string, Session, error) {
	now := s.Now()
	// Expired rows cannot log anyone in. This only keeps the table small, so
	// a failure must not stop the login.
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE idle_expires_at <= ? OR max_expires_at <= ?`, now.Unix(), now.Unix()); err != nil {
		log.Printf("sessions: cleanup: %v", err)
	}

	token := RandomToken()
	sess := Session{
		CSRF:         RandomToken(),
		GitHubID:     id.ID,
		Login:        id.Login,
		AvatarURL:    id.AvatarURL,
		CreatedAt:    now,
		RenewedAt:    now,
		MaxExpiresAt: now.Add(s.MaxTTL),
	}
	sess.ExpiresAt = earlier(now.Add(s.IdleTTL), sess.MaxExpiresAt)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, csrf, github_id, login, avatar_url, created_at, renewed_at, idle_expires_at, max_expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hash(token), sess.CSRF, sess.GitHubID, sess.Login, sess.AvatarURL,
		sess.CreatedAt.Unix(), sess.RenewedAt.Unix(), sess.ExpiresAt.Unix(), sess.MaxExpiresAt.Unix())
	if err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// Get finds a session that has not expired. It does not renew it.
func (s *Sessions) Get(ctx context.Context, token string) (Session, bool, error) {
	if token == "" {
		return Session{}, false, nil
	}
	now := s.Now().Unix()
	var sess Session
	var created, renewed, idle, max int64
	err := s.db.QueryRowContext(ctx,
		`SELECT csrf, github_id, login, avatar_url, created_at, renewed_at, idle_expires_at, max_expires_at
		 FROM sessions WHERE id_hash = ? AND idle_expires_at > ? AND max_expires_at > ?`,
		hash(token), now, now,
	).Scan(&sess.CSRF, &sess.GitHubID, &sess.Login, &sess.AvatarURL, &created, &renewed, &idle, &max)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	sess.CreatedAt = time.Unix(created, 0)
	sess.RenewedAt = time.Unix(renewed, 0)
	sess.ExpiresAt = time.Unix(idle, 0)
	sess.MaxExpiresAt = time.Unix(max, 0)
	return sess, true, nil
}

// Renew moves the idle deadline of a session from Get, if RenewEvery has
// passed since the last renewal. It reports true only when it changed the
// row; then the caller must send a cookie with the new deadline. The max
// deadline never moves.
func (s *Sessions) Renew(ctx context.Context, token string, sess Session) (Session, bool, error) {
	now := s.Now()
	if now.Sub(sess.RenewedAt) < s.RenewEvery {
		return sess, false, nil
	}
	idle := earlier(now.Add(s.IdleTTL), sess.MaxExpiresAt)
	// The renewed_at check makes sure that only one of many parallel
	// requests renews, and that a deleted session stays deleted.
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET renewed_at = ?, idle_expires_at = ? WHERE id_hash = ? AND renewed_at = ?`,
		now.Unix(), idle.Unix(), hash(token), sess.RenewedAt.Unix())
	if err != nil {
		return sess, false, err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return sess, false, err
	}
	sess.RenewedAt = time.Unix(now.Unix(), 0)
	sess.ExpiresAt = time.Unix(idle.Unix(), 0)
	return sess, true, nil
}

func (s *Sessions) Delete(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hash(token))
	return err
}

// DeleteAll ends every Admin page session, in every browser.
func (s *Sessions) DeleteAll(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
