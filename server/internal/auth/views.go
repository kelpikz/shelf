package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// StartView makes a short-lived code, bound to the browser that started the
// handoff. Only its hash is stored. The admin token never leaves its host.
func (s *Sessions) StartView(ctx context.Context, token, browser string) (string, error) {
	if browser == "" {
		return "", errors.New("missing browser state")
	}
	now := s.Now().Unix()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM view_codes WHERE expires_at <= ?`, now); err != nil {
		return "", err
	}
	code := RandomToken()
	result, err := s.db.ExecContext(ctx, `INSERT INTO view_codes(id_hash,browser_hash,session_hash,expires_at)
 SELECT ?,?,id_hash,? FROM sessions WHERE id_hash=? AND idle_expires_at>? AND max_expires_at>?`, hash(code), hash(browser), now+60, hash(token), now, now)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", errors.New("session ended")
	}
	return code, nil
}

// FinishView consumes the code and creates a view-only token in one transaction.
func (s *Sessions) FinishView(ctx context.Context, code, browser string) (string, time.Time, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, false, err
	}
	defer tx.Rollback()
	now := s.Now().Unix()
	var session string
	err = tx.QueryRowContext(ctx, `DELETE FROM view_codes WHERE id_hash=? AND browser_hash=? AND expires_at>? RETURNING session_hash`, hash(code), hash(browser), now).Scan(&session)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	var idle, max int64
	err = tx.QueryRowContext(ctx, `SELECT idle_expires_at,max_expires_at FROM sessions WHERE id_hash=? AND idle_expires_at>? AND max_expires_at>?`, session, now, now).Scan(&idle, &max)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	token := RandomToken()
	_, err = tx.ExecContext(ctx, `INSERT INTO view_sessions(id_hash,session_hash) VALUES(?,?)`, hash(token), session)
	if err != nil {
		return "", time.Time{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return "", time.Time{}, false, err
	}
	return token, earlier(time.Unix(idle, 0), time.Unix(max, 0)), true, nil
}

// CheckView follows the parent session on every request, so logout and expiry
// revoke access without needing to clear a cookie on the other host.
func (s *Sessions) CheckView(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	var found int
	now := s.Now().Unix()
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM view_sessions v JOIN sessions s ON s.id_hash=v.session_hash
 WHERE v.id_hash=? AND s.idle_expires_at>? AND s.max_expires_at>?`, hash(token), now, now).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
