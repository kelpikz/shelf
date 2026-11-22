package admin

import "net/http"

func (a *App) oauthConnections(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.session(w, r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	connections, err := a.OAuth.Connections(r.Context())
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.render(w, 200, "oauth-connections.html", map[string]any{"Connections": connections, "CSRF": sess.CSRF})
}

func (a *App) oauthRevokeConnection(w http.ResponseWriter, r *http.Request) {
	if !oauthForm(w, r) {
		return
	}
	if _, ok := a.checkPost(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	if !validOAuthSecret(id, 43, 43) {
		http.NotFound(w, r)
		return
	}
	if err := a.OAuth.RevokeConnection(r.Context(), id); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/oauth/connections", 303)
}
