// Package admin is the Admin page: login, logout, the list of Sites with
// their actions, and the approval page for CLI logins.
package admin

import (
	"html/template"
	"io/fs"
	"log"
	"net/http"

	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/sites"
)

type App struct {
	OwnerEmail      string
	OwnerGitHubID   int64
	AdminURL        string // origin of the admin host, for the Origin check
	FilesURL        string // origin of the Sites host, for the links to Sites
	Provider        auth.Provider
	Sessions        *auth.Sessions
	CLI             *auth.CLI
	Sites           *sites.Sites
	OAuth           *auth.OAuth
	MCPResource     string
	MCPClientID     string
	MCPRedirectURIs []string

	pages map[string]*template.Template
}

func New(ownerEmail string, ownerGitHubID int64, adminURL, filesURL string, p auth.Provider, s *auth.Sessions, cli *auth.CLI, st *sites.Sites) *App {
	return &App{
		OwnerEmail: ownerEmail, OwnerGitHubID: ownerGitHubID, AdminURL: adminURL, FilesURL: filesURL,
		Provider: p, Sessions: s, CLI: cli, Sites: st, pages: loadPages(),
	}
}

func (a *App) Handler() http.Handler {
	static, _ := fs.Sub(files, "static")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("GET /login", a.login)
	mux.HandleFunc("GET /view", a.view)
	mux.HandleFunc("GET /auth/github", a.startLogin)
	mux.HandleFunc("GET /auth/github/callback", a.finishLogin)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("POST /logout-all", a.logoutAll)
	mux.HandleFunc("POST /sites/{path}/{action}", a.siteAction)
	mux.HandleFunc("GET /cli/approve", a.cliApprovePage)
	mux.HandleFunc("POST /cli/approve/{step}", a.cliApproveStep)
	mux.HandleFunc("POST /cli/keys/{id}/revoke", a.revokeKey)
	mux.HandleFunc("POST /cli/keys/{id}/update", a.updateKey)
	if a.OAuth != nil && a.MCPResource != "" {
		mux.HandleFunc("GET /.well-known/oauth-authorization-server", a.oauthMetadata)
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", a.oauthResourceMetadata)
		mux.HandleFunc("GET /oauth/authorize", a.oauthAuthorize)
		mux.HandleFunc("POST /oauth/authorize", a.oauthDecision)
		mux.HandleFunc("POST /oauth/token", a.oauthToken)
		mux.HandleFunc("POST /oauth/revoke", a.oauthRevoke)
		mux.HandleFunc("GET /oauth/session", a.oauthSession)
		mux.HandleFunc("GET /oauth/connections", a.oauthConnections)
		mux.HandleFunc("POST /oauth/connections/{id}/revoke", a.oauthRevokeConnection)
	}
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

	// TODO: add paginaton
	all, err := a.Sites.List(r.Context())
	if err != nil {
		a.serverError(w, err)
		return
	}
	var live, archived []sites.Site
	for _, site := range all {
		if site.Archived {
			archived = append(archived, site)
		} else {
			live = append(live, site)
		}
	}
	keys, err := a.CLI.Keys(r.Context())
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.render(w, http.StatusOK, "sites.html", map[string]any{
		"Session": sess, "Sites": live, "Archived": archived, "Keys": keys, "FilesURL": a.FilesURL,
		"Now": a.CLI.Now(), "Error": siteErrors[r.URL.Query().Get("error")], "MCPEnabled": a.OAuth != nil && a.MCPResource != "",
	})
}
