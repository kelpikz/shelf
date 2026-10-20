package access

import (
	"context"
	"testing"
	"time"

	"github.com/kelpikz/shelf/server/internal/store"
)

const day = 24 * time.Hour

// newSessions gives a store with a clock that the test moves.
func newSessions(t *testing.T) (*Sessions, *time.Time) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewSessions(db)
	s.Now = func() time.Time { return now }
	return s, &now
}

// visit is one Admin page visit: Get, then Renew.
func visit(t *testing.T, s *Sessions, token string) (Session, bool) {
	t.Helper()
	ctx := context.Background()
	sess, ok, err := s.Get(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return Session{}, false
	}
	sess, _, err = s.Renew(ctx, token, sess)
	if err != nil {
		t.Fatal(err)
	}
	return sess, true
}

func TestSessionEndsAfterIdleTime(t *testing.T) {
	s, now := newSessions(t)
	token, _, _ := s.Create(context.Background(), Identity{ID: 1})

	*now = now.Add(30*day - time.Second)
	if _, ok := visit(t, s, token); !ok {
		t.Fatal("session ended before 30 idle days")
	}

	*now = now.Add(30 * day)
	if _, ok := visit(t, s, token); ok {
		t.Fatal("session still works after 30 idle days")
	}
}

func TestUseKeepsSessionUntilMaxTime(t *testing.T) {
	s, now := newSessions(t)
	token, created, _ := s.Create(context.Background(), Identity{ID: 1})

	// Use the page every 20 days. Each visit moves the idle deadline.
	for i := 0; i < 4; i++ {
		*now = now.Add(20 * day)
		sess, ok := visit(t, s, token)
		if !ok {
			t.Fatalf("session ended at day %d", (i+1)*20)
		}
		if sess.ExpiresAt.After(created.MaxExpiresAt) {
			t.Fatalf("idle deadline %v is after the max deadline", sess.ExpiresAt)
		}
	}

	// Day 90: the max deadline. Use does not help.
	*now = created.MaxExpiresAt
	if _, ok := visit(t, s, token); ok {
		t.Fatal("session still works at 90 days")
	}
}

func TestRenewAtMostOnceADay(t *testing.T) {
	s, now := newSessions(t)
	ctx := context.Background()
	token, _, _ := s.Create(ctx, Identity{ID: 1})

	*now = now.Add(23 * time.Hour)
	sess, _, _ := s.Get(ctx, token)
	if _, renewed, _ := s.Renew(ctx, token, sess); renewed {
		t.Fatal("renewed before one day")
	}

	*now = now.Add(2 * time.Hour)
	sess, _, _ = s.Get(ctx, token)
	got, renewed, err := s.Renew(ctx, token, sess)
	if err != nil || !renewed {
		t.Fatalf("did not renew after one day: %v", err)
	}
	if want := now.Add(30 * day).Truncate(time.Second); !got.ExpiresAt.Equal(want) {
		t.Errorf("idle deadline %v, want %v", got.ExpiresAt, want)
	}

	// A second request with the same old data must not renew again.
	if _, renewed, _ := s.Renew(ctx, token, sess); renewed {
		t.Error("two parallel requests both renewed")
	}
}

func TestRenewDoesNotBringBackDeletedSession(t *testing.T) {
	s, now := newSessions(t)
	ctx := context.Background()
	token, _, _ := s.Create(ctx, Identity{ID: 1})

	*now = now.Add(2 * day)
	sess, _, _ := s.Get(ctx, token)
	if err := s.Delete(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, renewed, _ := s.Renew(ctx, token, sess); renewed {
		t.Error("renewed a deleted session")
	}
	if _, ok, _ := s.Get(ctx, token); ok {
		t.Error("deleted session came back")
	}
}

func TestDeleteAll(t *testing.T) {
	s, _ := newSessions(t)
	ctx := context.Background()
	a, _, _ := s.Create(ctx, Identity{ID: 1})
	b, _, _ := s.Create(ctx, Identity{ID: 1})

	if n, err := s.DeleteAll(ctx); err != nil || n != 2 {
		t.Fatalf("DeleteAll: %d, %v", n, err)
	}
	for _, token := range []string{a, b} {
		if _, ok, _ := s.Get(ctx, token); ok {
			t.Error("session still works after DeleteAll")
		}
	}
}
