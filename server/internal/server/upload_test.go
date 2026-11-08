package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kelpikz/shelf/server/internal/api"
	"github.com/kelpikz/shelf/server/internal/sites"
)

func (ts *testServer) upload(path, key, kind string, archive io.Reader) *http.Response {
	r := httptest.NewRequest("PUT", adminURL+"/api/v1/sites/"+path+"?kind="+kind, archive)
	r.Header.Set("Content-Type", "application/gzip")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	return w.Result()
}

type heldBody struct {
	started, release chan struct{}
	archive          io.Reader
}

func (b *heldBody) Read(p []byte) (int, error) {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	<-b.release
	return b.archive.Read(p)
}

func TestBusyUploadLeavesCurrentSiteReadable(t *testing.T) {
	ts := newServer(t)
	key := ts.apiKey(t, "laptop")
	ts.publish(t, "notes", map[string]string{"index.html": "old"})
	ts.sites.SetVisibility(context.Background(), "notes", sites.Public)
	b := &heldBody{make(chan struct{}), make(chan struct{}), tarGz(t, map[string]string{"index.html": "new"})}
	done := make(chan *http.Response, 1)
	go func() { done <- ts.upload("notes", key, "folder", b) }()
	defer func() {
		close(b.release)
		resp := <-done
		if resp.StatusCode != 200 {
			t.Errorf("first upload: %d %s", resp.StatusCode, body(resp))
		}
	}()
	select {
	case <-b.started:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not start")
	}
	resp := ts.upload("other", key, "file", unreadableBody{})
	if resp.StatusCode != 503 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("busy: %d %s", resp.StatusCode, body(resp))
	}
	if got := body(ts.get(filesURL + "/notes/")); got != "old" {
		t.Fatalf("current Site while uploading: %q", got)
	}
}

func TestUploadDeadlineReleasesSlot(t *testing.T) {
	ts := newServer(t)
	key := ts.apiKey(t, "laptop")
	a := api.New(adminURL, filesURL, ts.cli, ts.sites)
	a.UploadTimeout = -time.Second // A deadline that has already passed.
	ts.h = a.Handler()
	resp := ts.upload("notes", key, "file", unreadableBody{})
	if resp.StatusCode != 408 {
		t.Fatalf("deadline: %d %s", resp.StatusCode, body(resp))
	}
	a.UploadTimeout = time.Second
	resp = ts.upload("notes", key, "file", tarGz(t, map[string]string{"a.txt": "ok"}))
	if resp.StatusCode != 200 {
		t.Fatalf("slot after timeout: %d %s", resp.StatusCode, body(resp))
	}
}

func TestSitesAPIUploadAndPathCheck(t *testing.T) {
	ts := newServer(t)
	key := ts.apiKey(t, "laptop")
	resp := ts.api("GET", "/sites/notes", key, nil)
	var check struct {
		Taken bool             `json:"taken"`
		Site  *json.RawMessage `json:"site"`
	}
	if resp.StatusCode != 200 {
		t.Fatalf("free path: %d %s", resp.StatusCode, body(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(&check); err != nil {
		t.Fatal(err)
	}
	if check.Taken || check.Site != nil {
		t.Fatalf("free path: %+v", check)
	}
	resp = ts.upload("notes", key, "folder", tarGz(t, map[string]string{"index.html": "first", "old.txt": "old"}))
	var site struct {
		Path, Kind, Visibility, URL string
		Archived                    bool
	}
	if resp.StatusCode != 200 {
		t.Fatalf("upload: %d %s", resp.StatusCode, body(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(&site); err != nil {
		t.Fatal(err)
	}
	if site.Path != "notes" || site.Kind != "folder" || site.Visibility != "private" || site.Archived || site.URL != filesURL+"/notes/" {
		t.Fatalf("new Site: %+v", site)
	}
	ctx := context.Background()
	if err := ts.sites.SetVisibility(ctx, "notes", sites.Public); err != nil {
		t.Fatal(err)
	}
	if err := ts.sites.SetArchived(ctx, "notes", true); err != nil {
		t.Fatal(err)
	}
	resp = ts.api("GET", "/sites/notes", key, nil)
	var taken struct {
		Taken bool
		Site  struct {
			Visibility string
			Archived   bool
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&taken); err != nil {
		t.Fatal(err)
	}
	if !taken.Taken || taken.Site.Visibility != "public" || !taken.Site.Archived {
		t.Fatalf("taken: %+v", taken)
	}
	resp = ts.upload("notes", key, "file", tarGz(t, map[string]string{"new.txt": "second"}))
	if resp.StatusCode != 200 {
		t.Fatalf("replace: %d %s", resp.StatusCode, body(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(&site); err != nil {
		t.Fatal(err)
	}
	if site.Visibility != "public" || !site.Archived || site.Kind != "file" || site.URL != filesURL+"/notes" {
		t.Fatalf("replacement: %+v", site)
	}
	current, _, _ := ts.sites.Get(ctx, "notes")
	if _, err := ts.sites.Open(current, "old.txt"); err == nil {
		t.Fatal("old file remains")
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
}

type unreadableBody struct{}

func (unreadableBody) Read([]byte) (int, error) { panic("unauthorized body was read") }

type timedOutBody struct{}

func (timedOutBody) Read([]byte) (int, error) { return 0, os.ErrDeadlineExceeded }

func TestUploadSocketTimeoutIsReportedAsTimeout(t *testing.T) {
	ts := newServer(t)
	key := ts.apiKey(t, "laptop")
	resp := ts.upload("notes", key, "file", timedOutBody{})
	if resp.StatusCode != 408 {
		t.Fatalf("socket timeout: %d %s", resp.StatusCode, body(resp))
	}
}

func TestInvalidArchivePayloadKeepsCurrentSite(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "truncated tar", true: "corrupt gzip"}[corrupt], func(t *testing.T) {
			ts := newServer(t)
			key := ts.apiKey(t, "laptop")
			old := ts.publish(t, "notes", map[string]string{"old.txt": "old"})
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			payload := strings.Repeat("x", 64<<10)
			if err := tw.WriteHeader(&tar.Header{Name: "new.txt", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if !corrupt {
				payload = payload[:5]
			}
			if _, err := tw.Write([]byte(payload)); err != nil {
				t.Fatal(err)
			}
			// Finish gzip without the tar end. The first case ends inside the
			// declared file. The second has a corrupt gzip checksum.
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			archive := buf.Bytes()
			if corrupt {
				archive[len(archive)-8] ^= 0xff
			}
			resp := ts.upload("notes", key, "file", bytes.NewReader(archive))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("payload: %d %s", resp.StatusCode, body(resp))
			}
			current, found, err := ts.sites.Get(context.Background(), "notes")
			if err != nil || !found || current.Version() != old.Version() {
				t.Fatalf("invalid upload changed Site: %v", err)
			}
		})
	}
}

func TestSitesAPIRequiresCLIKeyBeforeReading(t *testing.T) {
	ts := newServer(t)
	cookie, _ := ts.login(t)
	for _, method := range []string{"GET", "PUT"} {
		r := httptest.NewRequest(method, adminURL+"/api/v1/sites/notes?kind=file", unreadableBody{})
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/gzip")
		w := httptest.NewRecorder()
		ts.h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("%s: %d", method, w.Code)
		}
	}
	key := ts.apiKey(t, "revoked")
	ts.apiPost("/cli/logout", key, nil)
	if resp := ts.upload("notes", key, "file", unreadableBody{}); resp.StatusCode != 401 {
		t.Fatalf("revoked key: %d", resp.StatusCode)
	}
}

func TestSitesAPIRefusesInvalidRequests(t *testing.T) {
	ts := newServer(t)
	key := ts.apiKey(t, "laptop")
	for _, path := range []string{"Bad", "api"} {
		if resp := ts.api("GET", "/sites/"+path, key, nil); resp.StatusCode != 400 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
	}
	for _, tc := range []struct {
		kind, media string
		status      int
	}{
		{"other", "application/gzip", 400}, {"", "application/gzip", 400},
		{"file", "application/json", 415}, {"file", "application/gzip", 400},
	} {
		r := httptest.NewRequest("PUT", adminURL+"/api/v1/sites/notes?kind="+tc.kind, bytes.NewBufferString("invalid"))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", tc.media)
		w := httptest.NewRecorder()
		ts.h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: got %d %s", tc, w.Code, w.Body)
		}
	}
	ts.sites.MaxStorage = 1
	if resp := ts.upload("notes", key, "file", unreadableBody{}); resp.StatusCode != 507 {
		t.Fatalf("storage: %d %s", resp.StatusCode, body(resp))
	}
	ts.sites.MaxStorage = 0
	ts.sites.MaxUpload = 40
	r := httptest.NewRequest("PUT", adminURL+"/api/v1/sites/notes?kind=file", tarGz(t, map[string]string{"a.txt": "test"}))
	r.ContentLength = -1 // The actual body cap must also work without a length.
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/gzip")
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("oversized body: %d %s", w.Code, w.Body)
	}
}
