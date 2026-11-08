package admin

import (
	"net/http"
)

const viewNextCookie = "__Host-shelf_view_next"

func (a *App) view(w http.ResponseWriter, r *http.Request) {
	q, ok := readViewRequest(w, r)
	if !ok {
		return
	}
	token, ok := a.requireViewSession(w, r, q)
	if !ok {
		return
	}
	code, err := a.Sessions.StartView(r.Context(), token, q.Get("state"))
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.redirectToView(w, r, q, code)
}
