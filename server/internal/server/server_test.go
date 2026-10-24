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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/config"
	"github.com/kelpikz/shelf/server/internal/sites"
	"github.com/kelpikz/shelf/server/internal/store"
)

const (
	adminURL = "https://files-admin.example.com"
	filesURL = "https://files.example.com"
	owner    = "owner@example.com"
)

type fakeGitHub struct{}

func (fakeGitHub) LoginURL(state, verifier string) string { return "https://github.example/authorize" }
func (fakeGitHub) Finish(context.Context, string, string) (auth.Identity, error) {
	return auth.Identity{}, context.Canceled
}

type testServer struct {
	h        http.Handler
	sites    *sites.Sites
	sessions *auth.Sessions
	cli      *auth.CLI
}

func newServer(t *testing.T) *testServer {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Config{OwnerEmail: owner, OwnerGitHubID: 1, AdminURL: adminURL, FilesURL: filesURL, DataDir: dir}
	ts := &testServer{sites: sites.New(db, dir), sessions: auth.NewSessions(db), cli: auth.NewCLI(db)}
	ts.cli.PollEvery = 0
	ts.h = New(cfg, fakeGitHub{}, ts.sessions, ts.cli, ts.sites)
	return ts
}

// publish makes a Site at path from files (name to content), the same way as
// an upload. One entry that is not index.html makes a one-file Site.
func (ts *testServer) publish(t *testing.T, path string, files map[string]string) sites.Site {
	t.Helper()
	site, err := ts.sites.Publish(context.Background(), path, kindOf(files), tarGz(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return site
}

func kindOf(files map[string]string) sites.Kind {
	if _, ok := files["index.html"]; !ok && len(files) == 1 {
		return sites.File
	}
	return sites.Folder
}

// tarGz packs files into a .tar.gz, like the CLI does.
func tarGz(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return &buf
}

func (ts *testServer) get(target string) *http.Response {
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
	return w.Result()
}

// login gives a session cookie for the Owner and the CSRF token of the session.
func (ts *testServer) login(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	token, sess, err := ts.sessions.Create(context.Background(), auth.OwnerProfile{DisplayName: "ajitha"})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "__Host-shelf_session", Value: token}, sess.CSRF
}

// adminGet opens a page on the admin host with the session cookie.
func (ts *testServer) adminGet(c *http.Cookie, path string) *http.Response {
	r := httptest.NewRequest("GET", adminURL+path, nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	return w.Result()
}

// adminPost sends a form from the Admin page, with its Origin.
func (ts *testServer) adminPost(c *http.Cookie, origin, path string, form url.Values) *http.Response {
	r := httptest.NewRequest("POST", adminURL+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", origin)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	return w.Result()
}

// apiPost sends a JSON request to the CLI API, with the key if it is set.
func (ts *testServer) apiPost(path, key string, payload any) *http.Response {
	b, _ := json.Marshal(payload)
	r := httptest.NewRequest("POST", adminURL+"/api/v1"+path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	return w.Result()
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode %d response: %v", resp.StatusCode, err)
	}
	return v
}

type cliLogin struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
}

type pollAnswer struct {
	Status string `json:"status"`
	Key    string `json:"key"`
}

func body(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestVisitorOpensPublicFolderSite(t *testing.T) {
	ts := newServer(t)
	ts.publish(t, "notes", map[string]string{
		"index.html": "<h1>Notes</h1>",
		"css/a.css":  "h1 { color: red }",
	})
	if err := ts.sites.SetVisibility(context.Background(), "notes", sites.Public); err != nil {
		t.Fatal(err)
	}

	resp := ts.get(filesURL + "/notes")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/notes/" {
		t.Fatalf("/notes: got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp = ts.get(filesURL + "/notes/")
	if got := body(resp); resp.StatusCode != http.StatusOK || got != "<h1>Notes</h1>" {
		t.Fatalf("/notes/: got %d %q", resp.StatusCode, got)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "" {
		t.Errorf("public file has Cache-Control %q", cc)
	}
	if resp.Header.Get("Last-Modified") == "" {
		t.Error("public file has no Last-Modified")
	}

	resp = ts.get(filesURL + "/notes/css/a.css")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("css: got %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("css: missing nosniff")
	}
}

// A stranger must not find out which paths have a Private or archived Site.
func TestHiddenSitesLookLikeMissingOnes(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()
	ts.publish(t, "draft", map[string]string{"index.html": "draft"})
	ts.publish(t, "old", map[string]string{"index.html": "old"})
	ts.sites.SetVisibility(ctx, "old", sites.Public)
	if err := ts.sites.SetArchived(ctx, "old", true); err != nil {
		t.Fatal(err)
	}

	want := body(ts.get(filesURL + "/missing"))
	for _, target := range []string{"/draft", "/draft/", "/old", "/old/", "/missing", "/Draft", "/draft/../old/"} {
		resp := ts.get(filesURL + target)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: got %d %q", target, resp.StatusCode, resp.Header.Get("Location"))
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: missing no-store", target)
		}
		if got := body(resp); got != want {
			t.Errorf("%s: the page is different from the page for a missing Site", target)
		}
	}
	if !strings.Contains(want, `href="`+adminURL+`/login"`) {
		t.Error("the not found page has no Owner login link")
	}
}

func TestIndexPageListsOnlyPublicSites(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()
	ts.publish(t, "notes", map[string]string{"index.html": "notes"})
	ts.publish(t, "cv", map[string]string{"cv.pdf": "%PDF"})
	ts.publish(t, "draft", map[string]string{"index.html": "draft"})
	ts.publish(t, "old", map[string]string{"index.html": "old"})
	ts.sites.SetVisibility(ctx, "notes", sites.Public)
	ts.sites.SetVisibility(ctx, "cv", sites.Public)
	ts.sites.SetVisibility(ctx, "old", sites.Public)
	ts.sites.SetArchived(ctx, "old", true)

	resp := ts.get(filesURL + "/")
	page := body(resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	for _, link := range []string{`href="/notes/"`, `href="/cv"`} {
		if !strings.Contains(page, link) {
			t.Errorf("no link %s", link)
		}
	}
	for _, hidden := range []string{"draft", "old"} {
		if strings.Contains(page, "/"+hidden) {
			t.Errorf("%s is on the Index page", hidden)
		}
	}
}

func TestOneFileSiteIsServedAtItsPath(t *testing.T) {
	ts := newServer(t)
	ts.publish(t, "cv", map[string]string{"cv.pdf": "%PDF-1.7"})
	ts.sites.SetVisibility(context.Background(), "cv", sites.Public)

	resp := ts.get(filesURL + "/cv")
	if got := body(resp); resp.StatusCode != http.StatusOK || got != "%PDF-1.7" {
		t.Fatalf("got %d %q", resp.StatusCode, got)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type %q", ct)
	}
	if resp := ts.get(filesURL + "/cv/"); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/cv" {
		t.Errorf("/cv/: got %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp := ts.get(filesURL + "/cv/x"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("/cv/x: got %d", resp.StatusCode)
	}
}

func TestReuploadReplacesFilesAndKeepsSettings(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()
	first := ts.publish(t, "notes", map[string]string{"index.html": "v1", "old.txt": "old"})
	ts.sites.SetVisibility(ctx, "notes", sites.Public)

	second := ts.publish(t, "notes", map[string]string{"index.html": "v2"})
	if second.ID != first.ID || second.Visibility != sites.Public {
		t.Errorf("replace changed the Site: %+v", second)
	}
	if got := body(ts.get(filesURL + "/notes/")); got != "v2" {
		t.Errorf("got %q, want the new upload", got)
	}
	if resp := ts.get(filesURL + "/notes/old.txt"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a file of the old upload is still served: %d", resp.StatusCode)
	}
}

func TestUnarchivedSiteComesBack(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()
	ts.publish(t, "demo", map[string]string{"index.html": "demo"})
	ts.sites.SetVisibility(ctx, "demo", sites.Public)
	ts.sites.SetArchived(ctx, "demo", true)
	ts.sites.SetArchived(ctx, "demo", false)

	if resp := ts.get(filesURL + "/demo/"); resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestUnknownHostGetsNotFound(t *testing.T) {
	ts := newServer(t)
	ts.publish(t, "notes", map[string]string{"index.html": "notes"})
	ts.sites.SetVisibility(context.Background(), "notes", sites.Public)

	if resp := ts.get("https://evil.example.com/notes/"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestAdminPageListsSites(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()
	ts.publish(t, "notes", map[string]string{"index.html": "notes"})
	ts.publish(t, "old-demo", map[string]string{"index.html": "old"})
	ts.sites.SetArchived(ctx, "old-demo", true)
	c, _ := ts.login(t)

	resp := ts.adminGet(c, "/")
	page := body(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
	notes, archived := strings.Index(page, "notes"), strings.Index(page, "Archived")
	old := strings.Index(page, "old-demo")
	if notes < 0 || archived < 0 || old < archived {
		t.Errorf("want notes, then the Archived list with old-demo; got positions %d, %d, %d", notes, archived, old)
	}
	if !strings.Contains(page, filesURL+"/notes/") {
		t.Error("no link to the Site")
	}
}

func TestOwnerManagesSitesFromAdminPage(t *testing.T) {
	ts := newServer(t)
	ts.publish(t, "notes", map[string]string{"index.html": "notes"})
	c, csrf := ts.login(t)
	form := url.Values{"csrf": {csrf}}

	post := func(path string, form url.Values) {
		t.Helper()
		resp := ts.adminPost(c, adminURL, path, form)
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("%s: got %d", path, resp.StatusCode)
		}
	}
	status := func() int { return ts.get(filesURL + "/notes/").StatusCode }

	post("/sites/notes/public", form)
	if got := status(); got != http.StatusOK {
		t.Fatalf("after public: got %d", got)
	}
	post("/sites/notes/archive", form)
	if got := status(); got != http.StatusNotFound {
		t.Fatalf("after archive: got %d", got)
	}
	post("/sites/notes/unarchive", form)
	post("/sites/notes/private", form)
	if got := status(); got != http.StatusNotFound {
		t.Fatalf("after private: got %d", got)
	}

	post("/sites/notes/delete", url.Values{"csrf": {csrf}, "confirm": {"wrong"}})
	if _, ok, _ := ts.sites.Get(context.Background(), "notes"); !ok {
		t.Fatal("deleted with a wrong confirm")
	}
	post("/sites/notes/delete", url.Values{"csrf": {csrf}, "confirm": {"notes"}})
	if _, ok, _ := ts.sites.Get(context.Background(), "notes"); ok {
		t.Fatal("not deleted")
	}
}

func TestSiteActionsNeedOriginAndCSRF(t *testing.T) {
	ts := newServer(t)
	ts.publish(t, "notes", map[string]string{"index.html": "notes"})
	c, csrf := ts.login(t)

	if resp := ts.adminPost(c, filesURL, "/sites/notes/public", url.Values{"csrf": {csrf}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("other origin: got %d", resp.StatusCode)
	}
	if resp := ts.adminPost(c, adminURL, "/sites/notes/public", url.Values{"csrf": {"wrong"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong csrf: got %d", resp.StatusCode)
	}
	if got := ts.get(filesURL + "/notes/").StatusCode; got != http.StatusNotFound {
		t.Errorf("the Site became Public: %d", got)
	}
}

// Two uploads in one second have the same Last-Modified. The browser must
// still get the new files.
func TestBrowserGetsNewUploadInTheSameSecond(t *testing.T) {
	ts := newServer(t)
	now := time.Now()
	ts.sites.Now = func() time.Time { return now }
	ts.publish(t, "notes", map[string]string{"index.html": "v1"})
	ts.sites.SetVisibility(context.Background(), "notes", sites.Public)

	first := ts.get(filesURL + "/notes/")
	conditional := func() *http.Response {
		r := httptest.NewRequest("GET", filesURL+"/notes/", nil)
		r.Header.Set("If-Modified-Since", first.Header.Get("Last-Modified"))
		if etag := first.Header.Get("ETag"); etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		ts.h.ServeHTTP(w, r)
		return w.Result()
	}
	if resp := conditional(); resp.StatusCode != http.StatusNotModified {
		t.Fatalf("no change: got %d, want 304", resp.StatusCode)
	}

	ts.publish(t, "notes", map[string]string{"index.html": "v2"})
	resp := conditional()
	if got := body(resp); resp.StatusCode != http.StatusOK || got != "v2" {
		t.Fatalf("after a new upload: got %d %q", resp.StatusCode, got)
	}
}

// A replace must never make a Visitor see "not found" for a Site that exists.
func TestReplaceDoesNotBreakVisitors(t *testing.T) {
	ts := newServer(t)
	ts.publish(t, "notes", map[string]string{"index.html": "notes"})
	ts.sites.SetVisibility(context.Background(), "notes", sites.Public)

	files := map[string]string{"index.html": "notes"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			if _, err := ts.sites.Publish(context.Background(), "notes", sites.Folder, tarGz(t, files)); err != nil {
				t.Error(err)
				return
			}
		}
	}()

	var misses atomic.Int64
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				if resp := ts.get(filesURL + "/notes/"); resp.StatusCode != http.StatusOK {
					misses.Add(1)
				}
			}
		})
	}
	readers.Wait()
	if n := misses.Load(); n > 0 {
		t.Errorf("%d requests did not get the Site during replaces", n)
	}
}

func TestCLILoginOverAPI(t *testing.T) {
	ts := newServer(t)
	resp := ts.apiPost("/cli/login", "", map[string]string{"device_name": "laptop"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: got %d", resp.StatusCode)
	}
	login := decode[cliLogin](t, resp)
	if login.DeviceCode == "" || login.UserCode == "" || login.VerificationURI != adminURL+"/cli/approve" {
		t.Fatalf("login: %+v", login)
	}
	poll := func() pollAnswer {
		t.Helper()
		return decode[pollAnswer](t, ts.apiPost("/cli/login/poll", "", map[string]string{"device_code": login.DeviceCode}))
	}

	if got := poll(); got.Status != "wait" {
		t.Fatalf("before approval: %+v", got)
	}
	// The Owner types the code on the approval page, checks the device, and approves.
	c, csrf := ts.login(t)
	resp = ts.adminPost(c, adminURL, "/cli/approve/check", url.Values{"csrf": {csrf}, "code": {login.UserCode}})
	if page := body(resp); resp.StatusCode != http.StatusOK || !strings.Contains(page, "laptop") {
		t.Fatalf("check: got %d, device name missing", resp.StatusCode)
	}
	resp = ts.adminPost(c, adminURL, "/cli/approve/approve", url.Values{"csrf": {csrf}, "code": {login.UserCode}})
	if page := body(resp); resp.StatusCode != http.StatusOK || !strings.Contains(page, "CLI approved") {
		t.Fatalf("approve: got %d", resp.StatusCode)
	}
	got := poll()
	if got.Status != "approved" || got.Key == "" {
		t.Fatalf("after approval: %+v", got)
	}

	if resp := ts.apiPost("/cli/logout", got.Key, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: got %d", resp.StatusCode)
	}
	if resp := ts.apiPost("/cli/logout", got.Key, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("key works after logout: got %d", resp.StatusCode)
	}
}

func TestAPIRefusesSessionCookie(t *testing.T) {
	ts := newServer(t)
	c, _ := ts.login(t)
	r := httptest.NewRequest("POST", adminURL+"/api/v1/cli/logout", nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestCLIApprovalNeedsOwnerSession(t *testing.T) {
	ts := newServer(t)
	login, _ := ts.cli.StartLogin(context.Background(), "laptop", "")
	form := url.Values{"code": {login.UserCode}}

	r := httptest.NewRequest("POST", adminURL+"/cli/approve/approve", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", adminURL)
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Errorf("no session: got %d to %q", w.Code, w.Header().Get("Location"))
	}

	c, csrf := ts.login(t)
	form.Set("csrf", csrf)
	if resp := ts.adminPost(c, filesURL, "/cli/approve/approve", form); resp.StatusCode != http.StatusForbidden {
		t.Errorf("other origin: got %d", resp.StatusCode)
	}

	if s, _, _ := ts.cli.Poll(context.Background(), login.DeviceCode); s != auth.PollWait {
		t.Errorf("login was approved: %q", s)
	}
}

func TestOwnerCancelsCLIKey(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()
	login, _ := ts.cli.StartLogin(ctx, "work-pc", "")
	ts.cli.Approve(ctx, login.UserCode)
	_, key, _ := ts.cli.Poll(ctx, login.DeviceCode)
	k, _, _ := ts.cli.CheckKey(ctx, key)
	c, csrf := ts.login(t)

	if page := body(ts.adminGet(c, "/")); !strings.Contains(page, "work-pc") {
		t.Fatal("the key is not on the Admin page")
	}
	resp := ts.adminPost(c, adminURL, "/cli/keys/"+strconv.FormatInt(k.ID, 10)+"/revoke", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke: got %d", resp.StatusCode)
	}
	if _, ok, _ := ts.cli.CheckKey(ctx, key); ok {
		t.Fatal("key works after cancel")
	}
}
