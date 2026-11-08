package serving

import (
	"net/http"
	"time"

	"github.com/kelpikz/shelf/server/internal/auth"
)

func requireViewNavigation(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet || !auth.ViewNavigation(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// readViewCallback binds the callback to the browser that started it.
// On failure it writes the response without consuming the code.
func readViewCallback(w http.ResponseWriter, r *http.Request) (state, target string, ok bool) {
	if !requireViewNavigation(w, r) {
		return "", "", false
	}
	c, err := r.Cookie(viewStateCookie)
	target, ok = auth.ViewReturn(r.URL.Query().Get("return"))
	if err != nil || !ok || !auth.Equal(c.Value, r.URL.Query().Get("state")) {
		http.Error(w, "The viewing login did not work. Try again.", http.StatusForbidden)
		return "", "", false
	}
	return c.Value, target, true
}

func setViewStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name: viewStateCookie, Value: state, Path: "/", MaxAge: 600,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func setViewSessionCookies(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: viewCookie, Value: token, Path: "/", Expires: expires,
		MaxAge: int(time.Until(expires).Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: viewStateCookie, Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}
