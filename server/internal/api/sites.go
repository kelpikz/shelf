package api

import (
	"context"
	"errors"
	"mime"
	"net"
	"net/http"
	"time"

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
	if _, ok := a.key(w, r); !ok {
		return
	}
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
	if _, ok := a.key(w, r); !ok {
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/gzip" {
		writeError(w, http.StatusUnsupportedMediaType, "bad_request", "Send a .tar.gz with Content-Type: application/gzip.")
		return
	}
	if r.Header.Get("Content-Encoding") != "" {
		writeError(w, http.StatusUnsupportedMediaType, "bad_request", "Send raw gzip bytes without Content-Encoding.")
		return
	}
	query := r.URL.Query()["kind"]
	if len(query) != 1 || (query[0] != string(sites.File) && query[0] != string(sites.Folder)) {
		writeError(w, http.StatusBadRequest, "bad_upload", "Set kind=file or kind=folder.")
		return
	}
	if r.ContentLength > a.Sites.MaxUpload {
		siteError(w, sites.ErrUploadTooLarge)
		return
	}
	deadline := time.Now().Add(a.UploadTimeout)
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	// A context deadline alone cannot interrupt a blocked request-body read.
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		serverError(w, err)
		return
	}
	// Leave time to report a handler deadline to the client.
	if err := controller.SetWriteDeadline(deadline.Add(30 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		serverError(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.Sites.MaxUpload)
	site, err := a.Sites.Publish(ctx, r.PathValue("path"), sites.Kind(query[0]), r.Body)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
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
