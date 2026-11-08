package api

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/kelpikz/shelf/server/internal/sites"
)

// siteResponse keeps storage folder names out of the public API.
type siteResponse struct {
	Path       string           `json:"path"`
	Kind       sites.Kind       `json:"kind"`
	File       string           `json:"file"`
	Visibility sites.Visibility `json:"visibility"`
	Archived   bool             `json:"archived"`
	UpdatedAt  int64            `json:"updated_at"`
	URL        string           `json:"url"`
}

func (a *API) siteResponse(s sites.Site) siteResponse {
	return siteResponse{Path: s.Path, Kind: s.Kind, File: s.File, Visibility: s.Visibility,
		Archived: s.Archived, UpdatedAt: s.UpdatedAt.Unix(), URL: a.FilesURL + s.URL()}
}

func (a *API) checkPath(w http.ResponseWriter, r *http.Request) {
	site, taken, err := a.Sites.Check(r.Context(), r.PathValue("path"))
	if err != nil {
		siteError(w, err)
		return
	}
	var result *siteResponse
	if taken {
		s := a.siteResponse(site)
		result = &s
	}
	writeJSON(w, http.StatusOK, struct {
		Taken bool          `json:"taken"`
		Site  *siteResponse `json:"site"`
	}{taken, result})
}

func (a *API) uploadSite(w http.ResponseWriter, r *http.Request) {
	kind, ok := a.readUploadRequest(w, r)
	if !ok {
		return
	}
	r, cancel, err := a.limitUploadRequest(w, r)
	if err != nil {
		serverError(w, err)
		return
	}
	defer cancel()

	site, err := a.Sites.Publish(r.Context(), r.PathValue("path"), kind, r.Body)
	if err != nil {
		if r.Context().Err() != nil {
			err = r.Context().Err()
		}
		siteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.siteResponse(site))
}

func siteError(w http.ResponseWriter, err error) {
	var bodyLimit *http.MaxBytesError
	var timeout net.Error
	switch {
	case errors.Is(err, sites.ErrBadPath):
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
	case errors.Is(err, sites.ErrReserved):
		writeError(w, http.StatusBadRequest, "path_reserved", err.Error())
	case errors.Is(err, sites.ErrUploadBusy):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "upload_busy", "Another upload is in progress. Try again later.")
	case errors.Is(err, sites.ErrStorageFull):
		writeError(w, http.StatusInsufficientStorage, "storage_full", "Not enough storage for this upload.")
	case errors.Is(err, sites.ErrUploadTooLarge), errors.As(err, &bodyLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "upload_too_large", "The compressed upload is too large.")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), errors.As(err, &timeout) && timeout.Timeout():
		writeError(w, http.StatusRequestTimeout, "upload_timeout", "The upload stopped. Check the Site before trying again.")
	case errors.Is(err, sites.ErrBadUpload):
		writeError(w, http.StatusBadRequest, "bad_upload", "The archive is not valid or exceeds the file limits.")
	default:
		serverError(w, err)
	}
}
