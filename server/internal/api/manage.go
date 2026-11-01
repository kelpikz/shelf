package api

import (
	"errors"
	"net/http"

	"github.com/kelpikz/shelf/server/internal/sites"
)

func (a *API) listSites(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.key(w, r); !ok {
		return
	}
	all, err := a.Sites.List(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	result := make([]siteResponse, 0, len(all))
	for _, site := range all {
		if !site.Archived || r.URL.Query().Get("all") == "true" {
			result = append(result, a.siteResponse(site))
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) updateSite(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.key(w, r); !ok {
		return
	}
	var change sites.Changes
	if !readJSON(w, r, &change) {
		return
	}
	site, err := a.Sites.Change(r.Context(), r.PathValue("path"), change)
	if err != nil {
		managementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.siteResponse(site))
}

func (a *API) deleteSite(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.key(w, r); !ok {
		return
	}
	if err := a.Sites.Delete(r.Context(), r.PathValue("path")); err != nil {
		managementError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func managementError(w http.ResponseWriter, err error) {
	if errors.Is(err, sites.ErrBadChange) {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if errors.Is(err, sites.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No Site at this path.")
		return
	}
	serverError(w, err)
}
