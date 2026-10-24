// Package server joins the parts into one HTTP handler. It picks the part by
// the host name: the Sites host gets Serving, the admin host gets the Admin
// page and the API (under /api/), and any other host gets a 404.
package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/kelpikz/shelf/server/internal/admin"
	"github.com/kelpikz/shelf/server/internal/api"
	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/config"
	"github.com/kelpikz/shelf/server/internal/serving"
	"github.com/kelpikz/shelf/server/internal/sites"
)

func New(cfg config.Config, p auth.Provider, sessions *auth.Sessions, cli *auth.CLI, s *sites.Sites) http.Handler {
	adminHost, filesHost := host(cfg.AdminURL), host(cfg.FilesURL)
	adminApp := admin.New(cfg.OwnerEmail, cfg.OwnerGitHubID, cfg.AdminURL, cfg.FilesURL, p, sessions, s).Handler()
	cliAPI := api.New(cfg.AdminURL, cli).Handler()
	files := serving.New(s, cfg.AdminURL)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz":
			w.Write([]byte("ok\n"))
		case strings.EqualFold(r.Host, filesHost):
			files.ServeHTTP(w, r)
		case strings.EqualFold(r.Host, adminHost) && strings.HasPrefix(r.URL.Path, "/api/"):
			cliAPI.ServeHTTP(w, r)
		case strings.EqualFold(r.Host, adminHost):
			adminApp.ServeHTTP(w, r)
		default:
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
		}
	})
}

func host(origin string) string {
	u, _ := url.Parse(origin)
	return u.Host
}
