package admin

import (
	"log"
	"net/http"
	"time"

	"github.com/kelpikz/shelf/server/internal/auth"
)

const sessionCookie = "__Host-shelf_session"

// session gives the Owner's session. If the database fails, it logs the
// error and reports no session.
func (a *App) session(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.Session{}, false
	}
	sess, ok, err := a.Sessions.Get(r.Context(), c.Value)
	if err != nil {
		log.Printf("session: %v", err)
		return auth.Session{}, false
	}
	return sess, ok
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
