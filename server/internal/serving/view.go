package serving

import (
	"net/http"
	"net/url"
	"time"

	"github.com/kelpikz/shelf/server/internal/auth"
)

const viewCookie = "__Host-shelf_view"
const viewStateCookie = "__Host-shelf_view_state"

func (s *Serving) viewLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet || !auth.ViewNavigation(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	target, ok := auth.ViewReturn(r.URL.Query().Get("return"))
	if !ok {
		http.Error(w, "Invalid return path.", http.StatusBadRequest)
		return
	}
	state := auth.RandomToken()
	http.SetCookie(w, &http.Cookie{Name: viewStateCookie, Value: state, Path: "/", MaxAge: 600, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	q := url.Values{"state": {state}, "return": {target}}
	http.Redirect(w, r, s.AdminURL+"/view?"+q.Encode(), http.StatusFound)
}

func (s *Serving) finishView(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet || !auth.ViewNavigation(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	c, err := r.Cookie(viewStateCookie)
	target, ok := auth.ViewReturn(r.URL.Query().Get("return"))
	if err != nil || !ok || !auth.Equal(c.Value, r.URL.Query().Get("state")) {
		http.Error(w, "The viewing login did not work. Try again.", http.StatusForbidden)
		return
	}
	token, expires, ok, err := s.Sessions.FinishView(r.Context(), r.URL.Query().Get("code"), c.Value)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if !ok {
		http.Error(w, "The viewing login ended. Try again.", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: viewCookie, Value: token, Path: "/", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: viewStateCookie, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, target, http.StatusSeeOther)
}

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
