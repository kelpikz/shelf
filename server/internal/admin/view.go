package admin

import (
	"net/http"
	"net/url"

	"github.com/kelpikz/shelf/server/internal/auth"
)

const viewNextCookie = "__Host-shelf_view_next"

func (a *App) view(w http.ResponseWriter, r *http.Request) {
	if !auth.ViewNavigation(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	state := r.URL.Query().Get("state")
	target, ok := auth.ViewReturn(r.URL.Query().Get("return"))
	if !ok || len(state) != 43 {
		http.Error(w, "Invalid view request.", http.StatusBadRequest)
		return
	}
	q := url.Values{"state": {state}, "return": {target}}
	if _, ok := a.session(w, r); !ok {
		http.SetCookie(w, &http.Cookie{Name: viewNextCookie, Value: url.QueryEscape(q.Encode()), Path: "/", MaxAge: 600, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	c, _ := r.Cookie(sessionCookie)
	code, err := a.Sessions.StartView(r.Context(), c.Value, state)
	if err != nil {
		a.serverError(w, err)
		return
	}
	q.Set("code", code)
	http.Redirect(w, r, a.FilesURL+"/_shelf/view?"+q.Encode(), http.StatusSeeOther)
}

func viewContinuation(r *http.Request) string {
	c, err := r.Cookie(viewNextCookie)
	if err != nil {
		return "/"
	}
	raw, err := url.QueryUnescape(c.Value)
	if err != nil {
		return "/"
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "/"
	}
	target, ok := auth.ViewReturn(q.Get("return"))
	if !ok || len(q.Get("state")) != 43 {
		return "/"
	}
	return "/view?" + url.Values{"state": {q.Get("state")}, "return": {target}}.Encode()
}
