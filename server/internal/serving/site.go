package serving

import (
	"net/http"
	"strings"

	"github.com/kelpikz/shelf/server/internal/sites"
)

// authorizeSiteView lets everyone see a live Public Site. Only the signed-in
// Owner sees a Private or Archived Site; others get the not-found page.
func (s *Serving) authorizeSiteView(w http.ResponseWriter, r *http.Request, site sites.Site) bool {
	if site.Visibility == sites.Public && !site.Archived {
		return true
	}
	allowed, err := s.ownerView(r)
	if err != nil {
		s.serverError(w, err)
		return false
	}
	if !allowed {
		s.notFound(w, r)
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Referrer-Policy", "same-origin")
	return true
}

// resolveSiteFile applies the one-file and folder URL rules. It writes a
// redirect or not-found response when the URL does not name a file to open.
func (s *Serving) resolveSiteFile(w http.ResponseWriter, r *http.Request, site sites.Site) (string, bool) {
	_, rest, hasSlash := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if site.Kind == sites.File {
		if !hasSlash {
			return site.File, true
		}
		if rest == "" {
			redirect(w, r, site.URL())
		} else {
			s.notFound(w, r)
		}
		return "", false
	}
	// Links in the Site's HTML resolve from /path/, so send /path there.
	if !hasSlash {
		redirect(w, r, site.URL())
		return "", false
	}
	if rest == "" || strings.HasSuffix(rest, "/") {
		rest += "index.html"
	}
	return rest, true
}
