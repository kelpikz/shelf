package auth

import (
	"context"
	"testing"
	"time"

	"github.com/kelpikz/shelf/server/internal/store"
)

func TestViewAccessBoundToBrowserAndSession(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewSessions(db)
	ctx := context.Background()
	token, _, err := s.Create(ctx, OwnerProfile{})
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.StartView(ctx, token, "browser")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := s.FinishView(ctx, code, "other"); err != nil || ok {
		t.Fatalf("wrong browser: %v %v", ok, err)
	}
	view, _, ok, err := s.FinishView(ctx, code, "browser")
	if err != nil || !ok {
		t.Fatalf("finish: %v %v", ok, err)
	}
	if _, _, ok, err := s.FinishView(ctx, code, "browser"); err != nil || ok {
		t.Fatalf("replay: %v %v", ok, err)
	}
	if ok, err := s.CheckView(ctx, view); err != nil || !ok {
		t.Fatalf("view: %v %v", ok, err)
	}
	s.Delete(ctx, token)
	if ok, err := s.CheckView(ctx, view); err != nil || ok {
		t.Fatalf("logout: %v %v", ok, err)
	}
	token, _, _ = s.Create(ctx, OwnerProfile{})
	code, _ = s.StartView(ctx, token, "browser")
	now := s.Now()
	s.Now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, _, ok, err := s.FinishView(ctx, code, "browser"); err != nil || ok {
		t.Fatalf("expired code: %v %v", ok, err)
	}
	s.Now = func() time.Time { return now }
	code, _ = s.StartView(ctx, token, "browser")
	view, _, _, _ = s.FinishView(ctx, code, "browser")
	s.Now = func() time.Time { return now.Add(31 * 24 * time.Hour) }
	if ok, err := s.CheckView(ctx, view); err != nil || ok {
		t.Fatalf("expired session: %v %v", ok, err)
	}
}
