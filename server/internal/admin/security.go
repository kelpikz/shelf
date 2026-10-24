package admin

import (
	"net/http"

	"github.com/kelpikz/shelf/server/internal/auth"
)

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

// isPageVisit is true when the person opens the page in the browser tab. It
// is false for requests that a page on the Sites host makes, for example an
// <img> or an <iframe> that points at the admin host. Browsers without
// Sec-Fetch headers count as a page visit.
func isPageVisit(r *http.Request) bool {
	h := r.Header
	if h.Get("Sec-Fetch-Site") == "" && h.Get("Sec-Fetch-Mode") == "" {
		return true
	}
	return h.Get("Sec-Fetch-Mode") == "navigate" &&
		h.Get("Sec-Fetch-Dest") == "document" &&
		h.Get("Sec-Fetch-Site") != "same-site"
}

// sameOrigin is true when a state-changing request comes from the Admin page
// itself. The browser sets Origin and Sec-Fetch-Site, and scripts cannot
// change them. SameSite cookies do not help here, because the Sites host and
// the admin host are the same site.
func sameOrigin(r *http.Request, adminOrigin string) bool {
	if r.Header.Get("Origin") != adminOrigin {
		return false
	}
	site := r.Header.Get("Sec-Fetch-Site")
	return site == "" || site == "same-origin"
}

// checkPost checks the session, the Origin, and the CSRF token of a form
// post. If a check fails, it writes the response and reports false.
func (a *App) checkPost(w http.ResponseWriter, r *http.Request) (token string, ok bool) {
	sess, ok := a.session(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", false
	}
	if !sameOrigin(r, a.AdminURL) || !auth.Equal(r.PostFormValue("csrf"), sess.CSRF) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return "", false
	}
	c, _ := r.Cookie(sessionCookie)
	return c.Value, true
}
