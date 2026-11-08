package admin

import (
	"net/http"
	"net/url"

	"github.com/kelpikz/shelf/server/internal/auth"
)

// readViewRequest checks the browser navigation and the handoff fields.
// On failure it writes the response.
func readViewRequest(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	if !auth.ViewNavigation(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil, false
	}
	state := r.URL.Query().Get("state")
	target, ok := auth.ViewReturn(r.URL.Query().Get("return"))
	if !ok || len(state) != 43 {
		http.Error(w, "Invalid view request.", http.StatusBadRequest)
		return nil, false
	}
	return url.Values{"state": {state}, "return": {target}}, true
}

// requireViewSession saves the handoff before asking the Owner to log in.
func (a *App) requireViewSession(w http.ResponseWriter, r *http.Request, q url.Values) (string, bool) {
	if _, ok := a.session(w, r); !ok {
		http.SetCookie(w, &http.Cookie{
			Name: viewNextCookie, Value: url.QueryEscape(q.Encode()), Path: "/", MaxAge: 600,
			Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", false
	}
	c, _ := r.Cookie(sessionCookie)
	return c.Value, true
}

func (a *App) redirectToView(w http.ResponseWriter, r *http.Request, q url.Values, code string) {
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
