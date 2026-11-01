package admin

import (
	"log"
	"net/http"
	"strings"

	"github.com/kelpikz/shelf/server/internal/auth"
)

const loginCookie = "__Host-shelf_login" // state and PKCE verifier, for one login

var loginErrors = map[string]string{
	"cancelled": "You cancelled the login on GitHub.",
	"failed":    "The login did not work. Try again.",
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.session(w, r); ok {
		http.Redirect(w, r, viewContinuation(r), http.StatusSeeOther)
		return
	}
	a.render(w, http.StatusOK, "login.html", map[string]any{"Error": loginErrors[r.URL.Query().Get("error")]})
}

func (a *App) startLogin(w http.ResponseWriter, r *http.Request) {
	// A Site must not replace the login cookie of a login in progress.
	if !isPageVisit(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	state, verifier := auth.RandomToken(), auth.RandomToken()
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
	if !isPageVisit(r) {
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
	if !auth.Equal(state, q.Get("state")) || q.Get("code") == "" {
		http.Redirect(w, r, "/login?error=failed", http.StatusSeeOther)
		return
	}

	id, err := a.Provider.Finish(r.Context(), q.Get("code"), verifier)
	if err != nil {
		log.Printf("login: %v", err)
		http.Redirect(w, r, "/login?error=failed", http.StatusSeeOther)
		return
	}

	if !auth.IsOwner(id, a.OwnerEmail, a.OwnerGitHubID) {
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

	profile := auth.OwnerProfile{DisplayName: "@" + id.Login, AvatarURL: id.AvatarURL}
	token, sess, err := a.Sessions.Create(r.Context(), profile)
	if err != nil {
		a.serverError(w, err)
		return
	}
	setSessionCookie(w, token, sess.ExpiresAt)
	log.Printf("login: owner %s", id.Login)
	next := viewContinuation(r)
	clearCookie(w, viewNextCookie)
	http.Redirect(w, r, next, http.StatusSeeOther)
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
