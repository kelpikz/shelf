// Package admin is the Admin page: login, logout, and the list of Sites.
package admin

import (
	"html/template"
	"io/fs"
	"log"
	"net/http"

	"github.com/kelpikz/shelf/server/internal/auth"
)

type App struct {
	OwnerEmail    string
	OwnerGitHubID int64
	AdminURL      string // origin of the admin host, for the Origin check
	Provider      auth.Provider
	Sessions      *auth.Sessions

	pages map[string]*template.Template
}

func New(ownerEmail string, ownerGitHubID int64, adminURL string, p auth.Provider, s *auth.Sessions) *App {
	return &App{OwnerEmail: ownerEmail, OwnerGitHubID: ownerGitHubID, AdminURL: adminURL, Provider: p, Sessions: s, pages: loadPages()}
}

func (a *App) Handler() http.Handler {
	static, _ := fs.Sub(files, "static")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("GET /login", a.login)
	mux.HandleFunc("GET /auth/github", a.startLogin)
	mux.HandleFunc("GET /auth/github/callback", a.finishLogin)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("POST /logout-all", a.logoutAll)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	return securityHeaders(mux)
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.session(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	// Only a page visit by the person counts as use. If the renewal fails,
	// the old deadline stays.
	if isPageVisit(r) {
		c, _ := r.Cookie(sessionCookie)
		if renewed, ok, err := a.Sessions.Renew(r.Context(), c.Value, sess); err != nil {
			log.Printf("session: renew: %v", err)
		} else if ok {
			sess = renewed
			setSessionCookie(w, c.Value, sess.ExpiresAt)
		}
	}
	a.render(w, http.StatusOK, "sites.html", map[string]any{"Session": sess})
}
