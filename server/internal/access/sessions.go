package access

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// Session is a logged-in Owner on the admin page.
type Session struct {
	CSRF      string
	GitHubID  int64
	Login     string
	AvatarURL string
	ExpiresAt time.Time
}

// Sessions keeps sessions in SQLite. The table holds only a hash of each
// session token, so a copy of the database does not log anyone in.
type Sessions struct {
	db  *sql.DB
	TTL time.Duration
	Now func() time.Time
}

func NewSessions(db *sql.DB) *Sessions {
	return &Sessions{db: db, TTL: 7 * 24 * time.Hour, Now: time.Now}
}

// Create makes a session and gives the token for the cookie.
func (s *Sessions) Create(ctx context.Context, id Identity) (string, Session, error) {
	now := s.Now()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return "", Session{}, err
	}

	token := RandomToken()
	sess := Session{
		CSRF:      RandomToken(),
		GitHubID:  id.ID,
		Login:     id.Login,
		AvatarURL: id.AvatarURL,
		ExpiresAt: now.Add(s.TTL),
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, csrf, github_id, login, avatar_url, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		hash(token), sess.CSRF, sess.GitHubID, sess.Login, sess.AvatarURL, sess.ExpiresAt.Unix())
	if err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// Get finds a session that has not expired.
func (s *Sessions) Get(ctx context.Context, token string) (Session, bool, error) {
	if token == "" {
		return Session{}, false, nil
	}
	var sess Session
	var expires int64
	err := s.db.QueryRowContext(ctx,
		`SELECT csrf, github_id, login, avatar_url, expires_at FROM sessions WHERE id_hash = ? AND expires_at > ?`,
		hash(token), s.Now().Unix(),
	).Scan(&sess.CSRF, &sess.GitHubID, &sess.Login, &sess.AvatarURL, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	sess.ExpiresAt = time.Unix(expires, 0)
	return sess, true, nil
}

func (s *Sessions) Delete(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hash(token))
	return err
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
