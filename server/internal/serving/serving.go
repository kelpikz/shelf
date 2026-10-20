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
	"strings"

	"github.com/kelpikz/shelf/server/internal/sites"
)

//go:embed templates static
var files embed.FS

type Serving struct {
	Sites    *sites.Sites
	AdminURL string // for the "Owner? Log in" link

	pages  map[string]*template.Template
	static http.Handler
}

func New(s *sites.Sites, adminURL string) *Serving {
	sv := &Serving{Sites: s, AdminURL: adminURL, pages: map[string]*template.Template{}}
	for _, name := range []string{"index.html", "notfound.html"} {
		sv.pages[name] = template.Must(template.ParseFS(files, "templates/base.html", "templates/"+name))
	}
	static, _ := fs.Sub(files, "static")
	sv.static = http.StripPrefix("/_shelf/", http.FileServerFS(static))
	return sv
}

func (s *Serving) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Site paths never have "_", so a Site cannot take this address.
	if strings.HasPrefix(r.URL.Path, "/_shelf/") {
		w.Header().Set("Cache-Control", "no-cache")
		s.static.ServeHTTP(w, r)
		return
	}

	if r.URL.Path == "/" {
		s.index(w, r)
		return
	}

	// A replace deletes the old folder. If that happens between the record
	// lookup and the file open, look again: the record points to the new
	// folder. Three tries are enough for replaces that come one after another.
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
	path, rest, hasSlash := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	site, ok, err := s.Sites.Get(r.Context(), path)
	if err != nil {
		s.serverError(w, err)
		return false
	}
	if !ok || site.Archived || site.Visibility != sites.Public {
		s.notFound(w, r)
		return false
	}

	name := site.File
	if site.Kind == sites.File {
		if hasSlash {
			s.notFound(w, r)
			return false
		}
	} else {
		// Links in the Site's HTML resolve from /path/, so send /path there.
		if !hasSlash {
			redirect(w, r, "/"+path+"/")
			return false
		}
		name = rest
		if name == "" || strings.HasSuffix(name, "/") {
			name += "index.html"
		}
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

// index is the Index page: all Public Sites that are not archived.
func (s *Serving) index(w http.ResponseWriter, r *http.Request) {
	all, err := s.Sites.List(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	var public []sites.Site
	for _, site := range all {
		if site.Visibility == sites.Public && !site.Archived {
			public = append(public, site)
		}
	}
	s.render(w, http.StatusOK, "index.html", map[string]any{"Sites": public})
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
func (s *Serving) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusNotFound, "notfound.html", nil)
}

func (s *Serving) render(w http.ResponseWriter, status int, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["AdminURL"] = s.AdminURL
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
