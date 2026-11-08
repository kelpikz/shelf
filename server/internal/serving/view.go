package serving

import (
	"net/http"
	"net/url"

	"github.com/kelpikz/shelf/server/internal/auth"
)

const viewCookie = "__Host-shelf_view"
const viewStateCookie = "__Host-shelf_view_state"

func (s *Serving) viewLogin(w http.ResponseWriter, r *http.Request) {
	if !requireViewNavigation(w, r) {
		return
	}
	target, ok := auth.ViewReturn(r.URL.Query().Get("return"))
	if !ok {
		http.Error(w, "Invalid return path.", http.StatusBadRequest)
		return
	}
	state := auth.RandomToken()
	setViewStateCookie(w, state)
	q := url.Values{"state": {state}, "return": {target}}
	http.Redirect(w, r, s.AdminURL+"/view?"+q.Encode(), http.StatusFound)
}

func (s *Serving) finishView(w http.ResponseWriter, r *http.Request) {
	state, target, ok := readViewCallback(w, r)
	if !ok {
		return
	}
	token, expires, ok, err := s.Sessions.FinishView(r.Context(), r.URL.Query().Get("code"), state)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if !ok {
		http.Error(w, "The viewing login ended. Try again.", http.StatusForbidden)
		return
	}
	setViewSessionCookies(w, token, expires)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// auth for private sites
// returns true (when authenticated)
func (s *Serving) ownerView(r *http.Request) (bool, error) {
	// A top-level link may come from any host. Other requests must come from
	// this origin. Same-site sibling hosts do not count as the same origin.
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" && !auth.ViewNavigation(r) {
		return false, nil
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.FilesURL {
		return false, nil
	}
	c, err := r.Cookie(viewCookie)
	if err != nil {
		return false, nil
	}
	return s.Sessions.CheckView(r.Context(), c.Value)
}
