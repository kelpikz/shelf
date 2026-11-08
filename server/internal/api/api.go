// Package api is the JSON API for the CLI, under /api/v1/ on the admin host.
// It accepts only CLI keys (Authorization: Bearer), never the session cookie,
// so a web page cannot call it in the name of the Owner.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/sites"
)

const maxBody = 4 << 10 // JSON bodies are small; uploads get their own limit

type API struct {
	AdminURL      string // origin of the admin host, for the approval link
	CLI           *auth.CLI
	Sites         *sites.Sites
	FilesURL      string
	UploadTimeout time.Duration
}

func New(adminURL, filesURL string, cli *auth.CLI, s *sites.Sites) *API {
	return &API{AdminURL: adminURL, CLI: cli, Sites: s, FilesURL: filesURL, UploadTimeout: 120 * time.Second}
}

func (a *API) Handler() http.Handler {
	// Routes on protected need a valid key.
	protected := http.NewServeMux()
	protected.HandleFunc("POST /api/v1/cli/logout", a.logout)
	protected.HandleFunc("GET /api/v1/cli/key", a.keyInfo)
	protected.HandleFunc("PATCH /api/v1/cli/key", a.updateKey)
	protected.HandleFunc("GET /api/v1/sites/{path}", a.checkPath)
	protected.HandleFunc("GET /api/v1/sites", a.listSites)
	protected.HandleFunc("PATCH /api/v1/sites/{path}", a.updateSite)
	protected.HandleFunc("DELETE /api/v1/sites/{path}", a.deleteSite)
	protected.HandleFunc("PUT /api/v1/sites/{path}", a.uploadSite)
	protected.HandleFunc("/", notFound)

	// Open routes: the CLI has no key yet. More specific patterns win over /api/v1/.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/cli/login", a.startLogin)
	mux.HandleFunc("POST /api/v1/cli/login/poll", a.poll)
	mux.Handle("/api/v1/", a.requireKey(protected))
	mux.HandleFunc("/", notFound)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func (a *API) startLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceName string `json:"device_name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	login, err := a.CLI.StartLogin(r.Context(), req.DeviceName, clientIP(r))
	if errors.Is(err, auth.ErrTooManyLogins) {
		writeError(w, http.StatusTooManyRequests, "too_many_logins", err.Error())
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":      login.DeviceCode,
		"user_code":        login.UserCode,
		"verification_uri": a.AdminURL + "/cli/approve",
		"expires_in":       int(a.CLI.LoginTTL.Seconds()),
		"interval":         int(a.CLI.PollEvery.Seconds()),
	})
}

func (a *API) poll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceCode string `json:"device_code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	status, key, err := a.CLI.Poll(r.Context(), req.DeviceCode)
	if err != nil {
		serverError(w, err)
		return
	}
	resp := map[string]any{"status": string(status)}
	if key.Key != "" {
		resp["key"] = key.Key
		resp["device_name"] = key.DeviceName
		resp["expires_at"] = key.ExpiresAt.Unix()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.CLI.Revoke(r.Context(), keyFrom(r.Context()).ID); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// keyInfo describes the key that sends the request. A cancelled or ended key
// gets 401.
func (a *API) keyInfo(w http.ResponseWriter, r *http.Request) {
	writeKey(w, keyFrom(r.Context()))
}

// updateKey changes the key that sends the request. Only the KeyChanges
// fields are allowed in the body.
func (a *API) updateKey(w http.ResponseWriter, r *http.Request) {
	var ch auth.KeyChanges
	if !readJSON(w, r, &ch) {
		return
	}
	key, ok, err := a.CLI.UpdateKey(r.Context(), keyFrom(r.Context()).ID, ch)
	if errors.Is(err, auth.ErrBadName) {
		writeError(w, http.StatusBadRequest, "bad_name", auth.BadNameMessage)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok { // cancelled after the key check
		writeError(w, http.StatusUnauthorized, "unauthorized", "The key is not valid. Log in with: shelf login")
		return
	}
	writeKey(w, key)
}

func writeKey(w http.ResponseWriter, key auth.Key) {
	writeJSON(w, http.StatusOK, map[string]any{"device_name": key.DeviceName, "expires_at": key.ExpiresAt.Unix()})
}

type keyCtx struct{}

// requireKey runs next only for a valid Bearer key and puts the key in the
// request context. Otherwise key writes the error.
func (a *API) requireKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := a.key(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyCtx{}, key)))
	})
}

// keyFrom returns the key that requireKey checked. Outside requireKey it is empty.
func keyFrom(ctx context.Context) auth.Key {
	key, _ := ctx.Value(keyCtx{}).(auth.Key)
	return key
}

func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "No such API request.")
}

// key checks the Bearer key. If the check fails, it writes the response and
// reports false.
func (a *API) key(w http.ResponseWriter, r *http.Request) (auth.Key, bool) {
	token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Log in with: shelf login")
		return auth.Key{}, false
	}
	key, ok, err := a.CLI.CheckKey(r.Context(), token)
	if err != nil {
		serverError(w, err)
		return auth.Key{}, false
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "The key is not valid. Log in with: shelf login")
		return auth.Key{}, false
	}
	return key, true
}

// readJSON requires Content-Type: application/json. A cross-site form cannot
// send that type without a CORS preflight, and the API answers no preflight.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "bad_request", "Send JSON.")
		return false
	}
	// The whole body must fit in maxBody. Only the fields in v are allowed,
	// and nothing may follow the first value.
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err = dec.Decode(v); err == nil {
			if _, end := dec.Token(); end != io.EOF {
				err = errors.New("data after the JSON value")
			}
		}
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The JSON body is not valid.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("api: %v", err)
	writeError(w, http.StatusInternalServerError, "server_error", "Something went wrong.")
}

// clientIP is the IP of the client. Behind nginx, RemoteAddr is nginx, so the
// last X-Forwarded-For entry (the one nginx added) is used. The header is
// trusted only from a loopback or private address; a client on the internet
// can send any X-Forwarded-For. The result is shown to the Owner only.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || !(ip.IsLoopback() || ip.IsPrivate()) {
		return host
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return host
	}
	parts := strings.Split(xff, ",")
	return strings.TrimSpace(parts[len(parts)-1])
}
