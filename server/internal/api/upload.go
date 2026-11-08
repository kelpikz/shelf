package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/kelpikz/shelf/server/internal/sites"
)

// readUploadRequest checks the metadata without reading the body.
// On failure it writes the response.
func (a *API) readUploadRequest(w http.ResponseWriter, r *http.Request) (sites.Kind, bool) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/gzip" {
		writeError(w, http.StatusUnsupportedMediaType, "bad_request", "Send a .tar.gz with Content-Type: application/gzip.")
		return "", false
	}
	if r.Header.Get("Content-Encoding") != "" {
		writeError(w, http.StatusUnsupportedMediaType, "bad_request", "Send raw gzip bytes without Content-Encoding.")
		return "", false
	}
	query := r.URL.Query()["kind"]
	if len(query) != 1 || (query[0] != string(sites.File) && query[0] != string(sites.Folder)) {
		writeError(w, http.StatusBadRequest, "bad_upload", "Set kind=file or kind=folder.")
		return "", false
	}
	if r.ContentLength > a.Sites.MaxUpload {
		siteError(w, sites.ErrUploadTooLarge)
		return "", false
	}
	return sites.Kind(query[0]), true
}

// limitUploadRequest bounds the body and read time. The caller must cancel
// the returned context after publishing. Setup failures cancel it here.
func (a *API) limitUploadRequest(w http.ResponseWriter, r *http.Request) (*http.Request, context.CancelFunc, error) {
	deadline := time.Now().Add(a.UploadTimeout)
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	// A context deadline alone cannot interrupt a blocked request-body read.
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		cancel()
		return nil, nil, err
	}
	// Leave time to report a handler deadline to the client.
	if err := controller.SetWriteDeadline(deadline.Add(30 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		cancel()
		return nil, nil, err
	}
	r = r.WithContext(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, a.Sites.MaxUpload)
	return r, cancel, nil
}
