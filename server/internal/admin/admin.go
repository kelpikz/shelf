// Package admin is the Admin page: login, logout, and the list of Sites.
package admin

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/kelpikz/shelf/server/internal/access"
)

const (
	sessionCookie = "__Host-shelf_session"
	loginCookie   = "__Host-shelf_login" // state and PKCE verifier, for one login
)

//go:embed templates static
var files embed.FS

type App struct {
	OwnerEmail string
	AdminURL   string // origin of the admin host, for the Origin check
	Provider   access.Provider
	Sessions   *access.Sessions

	pages map[string]*template.Template
}

func New(ownerEmail, adminURL string, p access.Provider, s *access.Sessions) *App {
	a := &App{OwnerEmail: ownerEmail, AdminURL: adminURL, Provider: p, Sessions: s, pages: map[string]*template.Template{}}
	for _, name := range []string{"login.html", "thanks.html", "sites.html"} {
		a.pages[name] = template.Must(template.ParseFS(files, "templates/base.html", "templates/"+name))
	}
	return a
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

// securityHeaders applies to every admin response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self' https://avatars.githubusercontent.com; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		// Browsers ignore this header on plain http, so local dev still works.
		h.Set("Strict-Transport-Security", "max-age=31536000")
		next.ServeHTTP(w, r)
	})
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.session(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	// Only a page visit by the person counts as use. If the renewal fails,
	// the old deadline stays.
	if access.IsPageVisit(r) {
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

var loginErrors = map[string]string{
	"cancelled": "You cancelled the login on GitHub.",
	"failed":    "The login did not work. Try again.",
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.session(w, r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.render(w, http.StatusOK, "login.html", map[string]any{"Error": loginErrors[r.URL.Query().Get("error")]})
}

func (a *App) startLogin(w http.ResponseWriter, r *http.Request) {
	// A Site must not replace the login cookie of a login in progress.
	if !access.IsPageVisit(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	state, verifier := access.RandomToken(), access.RandomToken()
	// Lax, not Strict: the browser must send this cookie when GitHub sends
	// the person back to the callback.
	http.SetCookie(w, &http.Cookie{
		Name: loginCookie, Value: state + "." + verifier, Path: "/",
		MaxAge: 600, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, a.Provider.LoginURL(state, verifier), http.StatusFound)
}

func (a *App) finishLogin(w http.ResponseWriter, r *http.Request) {
	// A Site must not clear the login cookie of a login in progress.
	if !access.IsPageVisit(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	clearCookie(w, loginCookie)
	q := r.URL.Query()

	if q.Get("error") == "access_denied" {
		http.Redirect(w, r, "/login?error=cancelled", http.StatusSeeOther)
		return
	}

	c, err := r.Cookie(loginCookie)
	if err != nil {
		http.Redirect(w, r, "/login?error=failed", http.StatusSeeOther)
		return
	}
	state, verifier, _ := strings.Cut(c.Value, ".")
	if !access.Equal(state, q.Get("state")) || q.Get("code") == "" {
		http.Redirect(w, r, "/login?error=failed", http.StatusSeeOther)
		return
	}

	id, err := a.Provider.Finish(r.Context(), q.Get("code"), verifier)
	if err != nil {
		log.Printf("login: %v", err)
		http.Redirect(w, r, "/login?error=failed", http.StatusSeeOther)
		return
	}

	if !access.IsOwner(id, a.OwnerEmail) {
		// Store nothing about this person.
		a.render(w, http.StatusForbidden, "thanks.html", map[string]any{"Login": id.Login})
		return
	}

	// A new login replaces the session that this browser had before.
	if old, err := r.Cookie(sessionCookie); err == nil {
		if err := a.Sessions.Delete(r.Context(), old.Value); err != nil {
			log.Printf("login: delete old session: %v", err)
		}
	}

	token, sess, err := a.Sessions.Create(r.Context(), id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	setSessionCookie(w, token, sess.ExpiresAt)
	log.Printf("login: owner %s", id.Login)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	token, ok := a.checkPost(w, r)
	if !ok {
		return
	}
	if err := a.Sessions.Delete(r.Context(), token); err != nil {
		a.serverError(w, err)
		return
	}
	clearCookie(w, sessionCookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// logoutAll ends the Admin page session in every browser, this one too.
func (a *App) logoutAll(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.checkPost(w, r); !ok {
		return
	}
	n, err := a.Sessions.DeleteAll(r.Context())
	if err != nil {
		a.serverError(w, err)
		return
	}
	log.Printf("logout-all: ended %d sessions", n)
	clearCookie(w, sessionCookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// checkPost checks the session, the Origin, and the CSRF token of a form
// post. If a check fails, it writes the response and reports false.
func (a *App) checkPost(w http.ResponseWriter, r *http.Request) (token string, ok bool) {
	sess, ok := a.session(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", false
	}
	if !access.SameOrigin(r, a.AdminURL) || !access.Equal(r.PostFormValue("csrf"), sess.CSRF) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return "", false
	}
	c, _ := r.Cookie(sessionCookie)
	return c.Value, true
}

// session gives the Owner's session. If the database fails, it logs the
// error and reports no session.
func (a *App) session(w http.ResponseWriter, r *http.Request) (access.Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return access.Session{}, false
	}
	sess, ok, err := a.Sessions.Get(r.Context(), c.Value)
	if err != nil {
		log.Printf("session: %v", err)
		return access.Session{}, false
	}
	return sess, ok
}

func (a *App) render(w http.ResponseWriter, status int, page string, data any) {
	var buf bytes.Buffer
	if err := a.pages[page].ExecuteTemplate(&buf, "base", data); err != nil {
		a.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (a *App) serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "Something went wrong.", http.StatusInternalServerError)
}

func setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		Expires: expires, MaxAge: int(time.Until(expires).Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}
