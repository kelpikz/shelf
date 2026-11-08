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
	ErrBadPath        = errors.New("a path has 1 to 64 lower case letters, digits, and '-', and does not start with '-'")
	ErrReserved       = errors.New("this path is reserved")
	ErrBadUpload      = errors.New("bad upload")
	ErrUploadTooLarge = fmt.Errorf("%w: the upload is too large", ErrBadUpload)
	ErrUploadBusy     = errors.New("another upload is in progress")
	ErrStorageFull    = errors.New("not enough storage for an upload")
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

// Keep archive read errors distinct from filesystem write and close errors
// when io.Copy transfers a file. Preserve the cause for limits and timeouts.
type archiveReader struct{ io.Reader }

func (r archiveReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && err != io.EOF && !errors.Is(err, ErrBadUpload) {
		err = fmt.Errorf("%w: %w", ErrBadUpload, err)
	}
	return n, err
}

// tarOverhead is the space for the tar data around one entry: a 512-byte
// header and up to 511 bytes of padding, with space for a long name.
const tarOverhead = 2 << 10

// limitReader passes at most n bytes from r and returns ErrBadUpload(msg)
// past that. io.LimitReader returns io.EOF at the limit, which gzip and tar
// report as a truncated stream, not as an oversized one.
type limitReader struct {
	r   io.Reader
	n   int64  // bytes left; < 0 once the limit is exceeded
	msg string // error text
	err error  // optional error for this limit
}

// Read requests at most n+1 bytes, so exactly n bytes followed by EOF passes,
// and a single extra byte trips the limit. The error is sticky.
func (l *limitReader) Read(p []byte) (int, error) {
	if l.n < 0 {
		if l.err != nil {
			return 0, l.err
		}
		return 0, badUpload("%s", l.msg)
	}
	if int64(len(p)) > l.n+1 {
		p = p[:l.n+1]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	if l.n < 0 {
		if l.err != nil {
			return n, l.err
		}
		return n, badUpload("%s", l.msg)
	}
	return n, err
}
