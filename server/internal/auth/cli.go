package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

// PollStatus is the answer to a CLI poll. Names follow RFC 8628.
type PollStatus string

const (
	PollWait     PollStatus = "wait"
	PollSlowDown PollStatus = "slow_down"
	PollDenied   PollStatus = "denied"
	PollExpired  PollStatus = "expired" // also an unknown or used device code
	PollApproved PollStatus = "approved"
)

var (
	ErrBadCode       = errors.New("the code is wrong or expired")
	ErrTooManyTries  = errors.New("too many wrong codes, try again later")
	ErrTooManyLogins = errors.New("too many open CLI logins, try again later")
)

// KeyPrefix starts every CLI key, so a leaked key is easy to find.
const KeyPrefix = "shelf_"

// No vowels (no words), no look-alike characters. RFC 8628, section 6.1.
const userCodeChars = "BCDFGHJKLMNPQRSTVWXZ"

const userCodeLen = 8

// Login is a started CLI login.
type Login struct {
	DeviceCode string // secret; only the CLI sees it
	UserCode   string // "XXXX-XXXX"; the Owner types it on the approval page
	ExpiresAt  time.Time
}

// Key is a CLI key record. It never holds the key itself.
type Key struct {
	ID         int64
	DeviceName string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time
}

// CLI runs the device flow for the CLI and checks CLI keys. The tables hold
// only hashes of device codes and keys.
type CLI struct {
	db          *sql.DB
	LoginTTL    time.Duration
	PollEvery   time.Duration
	KeyTTL      time.Duration
	UseEvery    time.Duration // min gap between last_used_at writes
	MaxOpen     int           // open logins at one time
	MaxTries    int           // wrong codes in TriesWindow
	TriesWindow time.Duration
	Now         func() time.Time

	mu       sync.Mutex
	failures []time.Time // wrong codes; one Owner, so one global counter
}

func NewCLI(db *sql.DB) *CLI {
	return &CLI{
		db:          db,
		LoginTTL:    10 * time.Minute,
		PollEvery:   5 * time.Second,
		KeyTTL:      30 * 24 * time.Hour,
		UseEvery:    24 * time.Hour,
		MaxOpen:     5,
		MaxTries:    5,
		TriesWindow: 10 * time.Minute,
		Now:         time.Now,
	}
}

// StartLogin opens a login. deviceName and ip are untrusted; they are only
// shown to the Owner on the approval page.
func (c *CLI) StartLogin(ctx context.Context, deviceName, ip string) (Login, error) {
	now := c.Now().Unix()
	if _, err := c.db.ExecContext(ctx, `DELETE FROM cli_logins WHERE expires_at <= ?`, now); err != nil {
		return Login{}, err
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM cli_keys WHERE expires_at <= ?`, now); err != nil {
		return Login{}, err
	}

	var open int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cli_logins`).Scan(&open); err != nil {
		return Login{}, err
	}
	if open >= c.MaxOpen {
		return Login{}, ErrTooManyLogins
	}

	code := newUserCode()
	login := Login{
		DeviceCode: RandomToken(),
		UserCode:   code[:4] + "-" + code[4:],
		ExpiresAt:  time.Unix(now, 0).Add(c.LoginTTL),
	}
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO cli_logins (device_hash, user_code, device_name, ip, status, created_at, expires_at, polled_at)
		 VALUES (?, ?, ?, ?, 'pending', ?, ?, 0)`,
		hash(login.DeviceCode), code, cleanDeviceName(deviceName), ip, now, login.ExpiresAt.Unix())
	if err != nil {
		return Login{}, err
	}
	return login, nil
}

// Approve lets the CLI with this user code get a key on its next poll.
// The caller must check the Owner session and the CSRF token first.
func (c *CLI) Approve(ctx context.Context, userCode string) error {
	return c.decide(ctx, userCode, "approved")
}

// Deny ends the login. The CLI gets PollDenied.
func (c *CLI) Deny(ctx context.Context, userCode string) error {
	return c.decide(ctx, userCode, "denied")
}

func (c *CLI) decide(ctx context.Context, userCode, status string) error {
	now := c.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	kept := c.failures[:0]
	for _, f := range c.failures {
		if now.Sub(f) < c.TriesWindow {
			kept = append(kept, f)
		}
	}
	c.failures = kept
	if len(c.failures) >= c.MaxTries {
		return ErrTooManyTries
	}

	res, err := c.db.ExecContext(ctx,
		`UPDATE cli_logins SET status = ? WHERE user_code = ? AND status = 'pending' AND expires_at > ?`,
		status, normalizeUserCode(userCode), now.Unix())
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		c.failures = append(c.failures, now)
		return ErrBadCode
	}
	return nil
}

// Poll is the CLI asking about its login. On PollApproved it gives the key.
// The key is given once: the login row is deleted in the same transaction.
func (c *CLI) Poll(ctx context.Context, deviceCode string) (PollStatus, string, error) {
	now := c.Now()
	id := hash(deviceCode)
	var status, deviceName string
	var polledAt int64
	err := c.db.QueryRowContext(ctx,
		`SELECT status, device_name, polled_at FROM cli_logins WHERE device_hash = ? AND expires_at > ?`,
		id, now.Unix()).Scan(&status, &deviceName, &polledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PollExpired, "", nil
	}
	if err != nil {
		return "", "", err
	}

	if _, err := c.db.ExecContext(ctx, `UPDATE cli_logins SET polled_at = ? WHERE device_hash = ?`, now.Unix(), id); err != nil {
		return "", "", err
	}
	if polledAt != 0 && now.Sub(time.Unix(polledAt, 0)) < c.PollEvery {
		return PollSlowDown, "", nil
	}

	switch status {
	case "pending":
		return PollWait, "", nil
	case "denied":
		return PollDenied, "", nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM cli_logins WHERE device_hash = ? AND status = 'approved'`, id)
	if err != nil {
		return "", "", err
	}
	if n, err := res.RowsAffected(); err != nil {
		return "", "", err
	} else if n != 1 {
		// A parallel poll took the key.
		return PollExpired, "", nil
	}
	key := KeyPrefix + RandomToken()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO cli_keys (key_hash, device_name, created_at, expires_at, last_used_at) VALUES (?, ?, ?, ?, ?)`,
		hash(key), deviceName, now.Unix(), now.Add(c.KeyTTL).Unix(), now.Unix())
	if err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return PollApproved, key, nil
}

// CheckKey finds a CLI key that has not expired. A revoked key has no row.
func (c *CLI) CheckKey(ctx context.Context, key string) (Key, bool, error) {
	if !strings.HasPrefix(key, KeyPrefix) {
		return Key{}, false, nil
	}
	now := c.Now()
	var k Key
	var created, expires, used int64
	err := c.db.QueryRowContext(ctx,
		`SELECT id, device_name, created_at, expires_at, last_used_at FROM cli_keys WHERE key_hash = ? AND expires_at > ?`,
		hash(key), now.Unix()).Scan(&k.ID, &k.DeviceName, &created, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, false, nil
	}
	if err != nil {
		return Key{}, false, err
	}
	k.CreatedAt, k.ExpiresAt, k.LastUsedAt = time.Unix(created, 0), time.Unix(expires, 0), time.Unix(used, 0)

	if now.Sub(k.LastUsedAt) >= c.UseEvery {
		if _, err := c.db.ExecContext(ctx, `UPDATE cli_keys SET last_used_at = ? WHERE id = ?`, now.Unix(), k.ID); err != nil {
			return Key{}, false, err
		}
		k.LastUsedAt = time.Unix(now.Unix(), 0)
	}
	return k, true, nil
}

func newUserCode() string {
	b := make([]byte, 0, userCodeLen)
	buf := make([]byte, 1)
	for len(b) < userCodeLen {
		rand.Read(buf)
		// Reject the top of the byte range so each character is equally likely.
		if int(buf[0]) >= 256-256%len(userCodeChars) {
			continue
		}
		b = append(b, userCodeChars[int(buf[0])%len(userCodeChars)])
	}
	return string(b)
}

// normalizeUserCode accepts any case, spaces, and dashes.
func normalizeUserCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
}

func cleanDeviceName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	if r := []rune(s); len(r) > 64 {
		s = string(r[:64])
	}
	return s
}
