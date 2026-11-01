package sites

import (
	"context"
	"sync"
	"testing"
)

func TestChangeReturnsItsOwnWrite(t *testing.T) {
	s, _ := newSites(t)
	ctx := context.Background()
	if _, err := s.Publish(ctx, "draft", Folder, tarGz(t, entry{name: "index.html", body: "draft"})); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for _, visibility := range []Visibility{Public, Private} {
		workers.Add(1)
		go func(v Visibility) {
			defer workers.Done()
			for range 30 {
				result, err := s.Change(ctx, "draft", Changes{Visibility: &v})
				if err != nil {
					t.Error(err)
					return
				}
				if result.Visibility != v {
					t.Errorf("wrote %s, got %s", v, result.Visibility)
					return
				}
			}
		}(visibility)
	}
	workers.Wait()
}
