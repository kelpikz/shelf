// Package serving answers every request to the Sites host: the Index page,
// the "not found" page, and the files of each Site.
package serving

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/sites"
)

//go:embed templates static
var files embed.FS

type Serving struct {
	Sites    *sites.Sites
	AdminURL string // origin of the admin host, for the view handoff
	FilesURL string // origin of this host, for the Origin check
	Sessions *auth.Sessions

	pages  map[string]*template.Template
	static http.Handler
}

func New(s *sites.Sites, sessions *auth.Sessions, adminURL, filesURL string) *Serving {
	sv := &Serving{Sites: s, Sessions: sessions, AdminURL: adminURL, FilesURL: filesURL, pages: map[string]*template.Template{}}
	for _, name := range []string{"index.html", "notfound.html"} {
		sv.pages[name] = template.Must(template.ParseFS(files, "templates/base.html", "templates/"+name))
	}
	static, _ := fs.Sub(files, "static")
	sv.static = http.StripPrefix("/_shelf/", http.FileServerFS(static))
	return sv
}

// Handler routes the Sites host. "GET" patterns also match HEAD, and the
// mux answers other methods with 405 and an Allow header.
func (s *Serving) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_shelf/login", s.viewLogin)
	mux.HandleFunc("GET /_shelf/view", s.finishView)
	// Site paths never have "_", so a Site cannot take these addresses.
	mux.HandleFunc("GET /_shelf/", s.staticFile)
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /", s.site)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func (s *Serving) staticFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	s.static.ServeHTTP(w, r)
}

// site serves a file of a Site. A replace deletes the old folder. If that
// happens between the record lookup and the file open, look again: the
// record points to the new folder. Three tries are enough for replaces that
// come one after another.
func (s *Serving) site(w http.ResponseWriter, r *http.Request) {
	for range 3 {
		if !s.serveSite(w, r) {
			return
		}
	}
	s.notFound(w, r)
}

// serveSite serves a request for a Site. It reports true, and writes
// nothing, when the Site got new files while it ran.
func (s *Serving) serveSite(w http.ResponseWriter, r *http.Request) (again bool) {
	path, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	site, ok, err := s.Sites.Get(r.Context(), path)
	if err != nil {
		s.serverError(w, err)
		return false
	}
	if !ok {
		s.notFound(w, r)
		return false
	}
	if !s.authorizeSiteView(w, r, site) {
		return false
	}
	name, ok := s.resolveSiteFile(w, r, site)
	if !ok {
		return false
	}

	f, err := s.Sites.Open(site, name)
	if err != nil {
		if now, ok, _ := s.Sites.Get(r.Context(), path); ok && now.Version() != site.Version() {
			return true
		}
		s.notFound(w, r)
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		s.notFound(w, r)
		return false
	}
	// Last-Modified has whole seconds only, so two uploads in one second
	// share it. The ETag changes with each upload, and browsers check it
	// first.
	w.Header().Set("ETag", `"`+site.Version()+`"`)
	// The upload time, not the file time from the Owner's PC. An old file
	// time makes browsers keep a new upload for a long time.
	http.ServeContent(w, r, name, site.UpdatedAt, f)
	return false
}

// index is the Index page: all Sites that are not archived. Private Sites
// show only to a signed-in Owner.
func (s *Serving) index(w http.ResponseWriter, r *http.Request) {
	owner, err := s.ownerView(r)
	if err != nil {
		s.serverError(w, err)
		return
	}
	all, err := s.Sites.List(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	var shown []sites.Site
	for _, site := range all {
		if !site.Archived && (site.Visibility == sites.Public || owner) {
			shown = append(shown, site)
		}
	}
	s.render(w, r, http.StatusOK, "index.html", map[string]any{"Sites": shown, "Owner": owner})
}

// redirect is a 302 with no-store. Chrome keeps a 301 for ever.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.URL.RawQuery != "" {
		to += "?" + r.URL.RawQuery
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusFound)
}

// notFound is the one page for a missing, archived, or Private Site.
// A failed Owner check only shows the sign in link.
func (s *Serving) notFound(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.ownerView(r)
	s.render(w, r, http.StatusNotFound, "notfound.html", map[string]any{"Owner": owner})
}

func (s *Serving) render(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["ViewLoginURL"] = "/_shelf/login?" + url.Values{"return": {r.URL.RequestURI()}}.Encode()
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, "base", data); err != nil {
		s.serverError(w, err)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Serving) serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "Something went wrong.", http.StatusInternalServerError)
}
