package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kelpikz/shelf/server/internal/store"
)

// newCLI gives a CLI with a clock that the test moves.
func newCLI(t *testing.T) (*CLI, *time.Time) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := NewCLI(db)
	c.Now = func() time.Time { return now }
	return c, &now
}

func poll(t *testing.T, c *CLI, deviceCode string) (PollStatus, string) {
	t.Helper()
	status, key, err := c.Poll(context.Background(), deviceCode)
	if err != nil {
		t.Fatal(err)
	}
	return status, key.Key
}

func TestCLILoginGivesKeyOnce(t *testing.T) {
	c, now := newCLI(t)
	ctx := context.Background()
	login, err := c.StartLogin(ctx, "laptop", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}

	if s, _ := poll(t, c, login.DeviceCode); s != PollWait {
		t.Fatalf("before approval: %q, want wait", s)
	}
	if s, _ := poll(t, c, login.DeviceCode); s != PollSlowDown {
		t.Fatalf("poll without a pause: %q, want slow_down", s)
	}

	// The Owner types the code in lower case, with the dash.
	if err := c.Approve(ctx, "  "+strings.ToLower(login.UserCode)+" ", ""); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(c.PollEvery)
	s, key := poll(t, c, login.DeviceCode)
	if s != PollApproved || key == "" {
		t.Fatalf("after approval: %q, key %q", s, key)
	}
	if _, ok, err := c.CheckKey(ctx, key); err != nil || !ok {
		t.Fatalf("new key refused: %v", err)
	}

	*now = now.Add(c.PollEvery)
	if s, key := poll(t, c, login.DeviceCode); s != PollExpired || key != "" {
		t.Fatalf("second poll after approval: %q, key %q", s, key)
	}
}

func TestDeniedLoginGivesNoKey(t *testing.T) {
	c, _ := newCLI(t)
	ctx := context.Background()
	login, _ := c.StartLogin(ctx, "laptop", "")
	if err := c.Deny(ctx, login.UserCode); err != nil {
		t.Fatal(err)
	}
	if s, key := poll(t, c, login.DeviceCode); s != PollDenied || key != "" {
		t.Fatalf("poll: %q, key %q", s, key)
	}
	if err := c.Approve(ctx, login.UserCode, ""); !errors.Is(err, ErrBadCode) {
		t.Fatalf("approve after deny: %v, want ErrBadCode", err)
	}
}

func TestWrongOrExpiredCodeRefused(t *testing.T) {
	c, now := newCLI(t)
	ctx := context.Background()
	login, _ := c.StartLogin(ctx, "laptop", "")

	if err := c.Approve(ctx, "BCDF-GHJK", ""); !errors.Is(err, ErrBadCode) {
		t.Fatalf("wrong code: %v, want ErrBadCode", err)
	}

	*now = now.Add(c.LoginTTL)
	if err := c.Approve(ctx, login.UserCode, ""); !errors.Is(err, ErrBadCode) {
		t.Fatalf("expired code: %v, want ErrBadCode", err)
	}
	if s, _ := poll(t, c, login.DeviceCode); s != PollExpired {
		t.Fatalf("poll after expiry: %q", s)
	}
}

func TestTooManyWrongCodes(t *testing.T) {
	c, now := newCLI(t)
	ctx := context.Background()
	login, _ := c.StartLogin(ctx, "laptop", "")
	for i := 0; i < c.MaxTries; i++ {
		c.Approve(ctx, "BCDF-GHJK", "")
	}
	if err := c.Approve(ctx, login.UserCode, ""); !errors.Is(err, ErrTooManyTries) {
		t.Fatalf("right code after %d wrong codes: %v, want ErrTooManyTries", c.MaxTries, err)
	}

	*now = now.Add(c.TriesWindow)
	login, _ = c.StartLogin(ctx, "laptop", "")
	if err := c.Approve(ctx, login.UserCode, ""); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func TestMaxOpenLogins(t *testing.T) {
	c, now := newCLI(t)
	ctx := context.Background()
	for i := 0; i < c.MaxOpen; i++ {
		if _, err := c.StartLogin(ctx, "laptop", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.StartLogin(ctx, "laptop", ""); !errors.Is(err, ErrTooManyLogins) {
		t.Fatalf("login %d: %v, want ErrTooManyLogins", c.MaxOpen+1, err)
	}

	*now = now.Add(c.LoginTTL)
	if _, err := c.StartLogin(ctx, "laptop", ""); err != nil {
		t.Fatalf("after the old logins expired: %v", err)
	}
}

func TestKeyEndsAfter30Days(t *testing.T) {
	c, now := newCLI(t)
	ctx := context.Background()
	key := approvedKey(t, c)

	*now = now.Add(30*day - time.Second)
	if _, ok, _ := c.CheckKey(ctx, key); !ok {
		t.Fatal("key ended before 30 days")
	}
	*now = now.Add(time.Second)
	if _, ok, _ := c.CheckKey(ctx, key); ok {
		t.Fatal("key still works at 30 days")
	}
	if _, ok, _ := c.CheckKey(ctx, "shelf_wrong"); ok {
		t.Fatal("unknown key accepted")
	}
}

func TestRevokedKeyRefused(t *testing.T) {
	c, _ := newCLI(t)
	ctx := context.Background()
	a, b := approvedKey(t, c), approvedKey(t, c)

	keys, err := c.Keys(ctx)
	if err != nil || len(keys) != 2 {
		t.Fatalf("Keys: %d keys, %v", len(keys), err)
	}
	ka, _, _ := c.CheckKey(ctx, a)
	if err := c.Revoke(ctx, ka.ID); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := c.CheckKey(ctx, a); ok {
		t.Error("revoked key still works")
	}
	if _, ok, _ := c.CheckKey(ctx, b); !ok {
		t.Error("revoke cancelled the other key too")
	}
	if keys, _ := c.Keys(ctx); len(keys) != 1 || keys[0].ID == ka.ID {
		t.Errorf("Keys after revoke: %+v", keys)
	}
}

// A stale Cancel form sends the ID of a key that is already gone. It must not
// hit a newer key.
func TestRevokedIDIsNotReused(t *testing.T) {
	c, _ := newCLI(t)
	ctx := context.Background()
	old, _, _ := c.CheckKey(ctx, approvedKey(t, c))
	c.Revoke(ctx, old.ID)

	key := approvedKey(t, c)
	c.Revoke(ctx, old.ID)
	if _, ok, _ := c.CheckKey(ctx, key); !ok {
		t.Fatal("revoking the old ID cancelled the new key")
	}
}

func TestMaxOpenLoginsUnderParallelStarts(t *testing.T) {
	c, _ := newCLI(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	started := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.StartLogin(context.Background(), "laptop", ""); err == nil {
				mu.Lock()
				started++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if started != c.MaxOpen {
		t.Fatalf("%d logins started, want %d", started, c.MaxOpen)
	}
}

func approvedKey(t *testing.T, c *CLI) string {
	t.Helper()
	ctx := context.Background()
	login, err := c.StartLogin(ctx, "laptop", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Approve(ctx, login.UserCode, ""); err != nil {
		t.Fatal(err)
	}
	_, key := poll(t, c, login.DeviceCode)
	if key == "" {
		t.Fatal("no key")
	}
	return key
}

func TestApprovalSetsDeviceName(t *testing.T) {
	c, now := newCLI(t)
	ctx := context.Background()
	for _, tc := range []struct{ typed, want string }{
		{"  Work laptop ", "Work laptop"},
		{"", "DESKTOP-HQOQ832"}, // empty keeps the name that the CLI sent
		{"   ", "DESKTOP-HQOQ832"},
	} {
		login, _ := c.StartLogin(ctx, "DESKTOP-HQOQ832", "")
		if err := c.Approve(ctx, login.UserCode, tc.typed); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(c.PollEvery)
		_, key, err := c.Poll(ctx, login.DeviceCode)
		if err != nil || key.DeviceName != tc.want {
			t.Fatalf("typed %q: device name %q, want %q (%v)", tc.typed, key.DeviceName, tc.want, err)
		}
	}

	// A bad name is not a wrong code, and the login stays open.
	login, _ := c.StartLogin(ctx, "laptop", "")
	if err := c.Approve(ctx, login.UserCode, strings.Repeat("a", 65)); !errors.Is(err, ErrBadName) {
		t.Fatalf("long name: %v", err)
	}
	if s, _ := poll(t, c, login.DeviceCode); s != PollWait {
		t.Fatalf("after bad name: %q, want wait", s)
	}
}

func TestRenameKey(t *testing.T) {
	c, _ := newCLI(t)
	ctx := context.Background()
	key := approvedKey(t, c)
	k, _, _ := c.CheckKey(ctx, key)

	got, err := c.Rename(ctx, k.ID, " Home PC ")
	if err != nil || got != "Home PC" {
		t.Fatalf("rename: %q, %v", got, err)
	}
	if k, _, _ := c.CheckKey(ctx, key); k.DeviceName != "Home PC" {
		t.Fatalf("after rename: %q", k.DeviceName)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("é", 65), "a\x1b[31mb", "a\nb"} {
		if _, err := c.Rename(ctx, k.ID, bad); !errors.Is(err, ErrBadName) {
			t.Errorf("rename to %q: %v, want ErrBadName", bad, err)
		}
	}
	if k, _, _ := c.CheckKey(ctx, key); k.DeviceName != "Home PC" {
		t.Fatalf("a bad name changed the key: %q", k.DeviceName)
	}
}
