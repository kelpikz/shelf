package sites

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
)

var (
	ErrBadPath   = errors.New("a path has 1 to 64 lower case letters, digits, and '-', and does not start with '-'")
	ErrReserved  = errors.New("this path is reserved")
	ErrBadUpload = errors.New("bad upload")
)

var validPath = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// reserved stops new Sites only. A Site that has a name before the name goes
// on this list keeps working. The server's own addresses have "_" or ".", so
// they need no place here.
var reserved = []string{
	"admin", "api", "assets", "auth", "favicon", "index", "healthz", "login",
	"logout", "robots", "shelf", "sitemap", "static", "www",
}

// Check tells if path can have a Site: it is bad (an error), free, or taken
// by the Site that it gives back.
func (s *Sites) Check(ctx context.Context, path string) (Site, bool, error) {
	if !validPath.MatchString(path) {
		return Site{}, false, ErrBadPath
	}
	site, taken, err := s.Get(ctx, path)
	if err != nil || taken {
		return site, taken, err
	}
	if slices.Contains(reserved, path) {
		return Site{}, false, ErrReserved
	}
	return Site{}, false, nil
}

func badUpload(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadUpload, fmt.Sprintf(format, args...))
}

// limitReader fails with ErrBadUpload when more than n bytes come. A plain
// io.LimitReader stops quietly, and the upload looks broken, not too large.
type limitReader struct {
	r io.Reader
	n int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.n < 0 {
		return 0, badUpload("the upload is too large")
	}
	if int64(len(p)) > l.n+1 {
		p = p[:l.n+1]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	if l.n < 0 {
		return n, badUpload("the upload is too large")
	}
	return n, err
}
