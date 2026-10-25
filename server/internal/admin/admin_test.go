package admin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/sites"
	"github.com/kelpikz/shelf/server/internal/store"
)

const (
	adminURL = "https://files-admin.example.com"
	owner    = "owner@example.com"
)

// fakeGitHub gives back a fixed identity and checks the PKCE verifier.
type fakeGitHub struct {
	id       auth.Identity
	verifier string
}

func (f *fakeGitHub) LoginURL(state, verifier string) string {
	f.verifier = verifier
	return "https://github.example/authorize?state=" + url.QueryEscape(state)
}

func (f *fakeGitHub) Finish(_ context.Context, code, verifier string) (auth.Identity, error) {
	if verifier != f.verifier {
		return auth.Identity{}, context.Canceled
	}
	return f.id, nil
}

const filesURL = "https://files.example.com"

func newApp(t *testing.T, id auth.Identity) http.Handler {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(owner, 1, adminURL, filesURL, &fakeGitHub{id: id}, auth.NewSessions(db), auth.NewCLI(db), sites.New(db, dir)).Handler()
}

var ownerID = auth.Identity{ID: 1, Login: "ajitha", Emails: []auth.Email{{Address: owner, Verified: true}}}

func do(h http.Handler, r *http.Request) *http.Response {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Result()
}

func cookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// login runs the GitHub flow and gives the callback response.
func login(t *testing.T, h http.Handler, stateOverride string) *http.Response {
	t.Helper()
	start := do(h, httptest.NewRequest("GET", "/auth/github", nil))
	if start.StatusCode != http.StatusFound {
		t.Fatalf("start: status %d", start.StatusCode)
	}
	loc, _ := url.Parse(start.Header.Get("Location"))
	state := loc.Query().Get("state")
	if stateOverride != "" {
		state = stateOverride
	}

	r := httptest.NewRequest("GET", "/auth/github/callback?code=abc&state="+url.QueryEscape(state), nil)
	r.AddCookie(cookie(start, loginCookie))
	return do(h, r)
}

func TestHomeWithoutSessionGoesToLogin(t *testing.T) {
	resp := do(newApp(t, ownerID), httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestOwnerLogsIn(t *testing.T) {
	h := newApp(t, ownerID)
	resp := login(t, h, "")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("callback: got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	c := cookie(resp, sessionCookie)
	if c == nil {
		t.Fatal("no session cookie")
	}
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Errorf("bad cookie flags: %+v", c)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	home := do(h, r)
	body := readBody(home)
	if home.StatusCode != http.StatusOK || !strings.Contains(body, "@ajitha") {
		t.Fatalf("home: got %d", home.StatusCode)
	}
	if home.Header.Get("Cache-Control") != "no-store" {
		t.Error("home: missing no-store")
	}
}

func TestUnverifiedEmailGetsThanks(t *testing.T) {
	stranger := auth.Identity{ID: 1, Login: "stranger", Emails: []auth.Email{{Address: owner, Verified: false}}}
	resp := login(t, newApp(t, stranger), "")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(readBody(resp), "Thanks") {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if cookie(resp, sessionCookie) != nil {
		t.Error("stranger got a session cookie")
	}
}

func TestOtherAccountWithOwnerEmailGetsThanks(t *testing.T) {
	stranger := auth.Identity{ID: 2, Login: "stranger", Emails: []auth.Email{{Address: owner, Verified: true}}}
	resp := login(t, newApp(t, stranger), "")
	if resp.StatusCode != http.StatusForbidden || cookie(resp, sessionCookie) != nil {
		t.Fatalf("got %d and a session cookie", resp.StatusCode)
	}
}

func TestWrongStateIsRefused(t *testing.T) {
	resp := login(t, newApp(t, ownerID), "not-the-state")
	if resp.Header.Get("Location") != "/login?error=failed" {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if cookie(resp, sessionCookie) != nil {
		t.Error("got a session cookie with a wrong state")
	}
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func TestLogoutNeedsOriginAndCSRF(t *testing.T) {
	h := newApp(t, ownerID)
	sess := cookie(login(t, h, ""), sessionCookie)

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(sess)
	m := csrfField.FindStringSubmatch(readBody(do(h, r)))
	if m == nil {
		t.Fatal("no csrf field on the Admin page")
	}
	csrf := m[1]

	logout := func(origin, token string) *http.Response {
		r := httptest.NewRequest("POST", "/logout", strings.NewReader("csrf="+url.QueryEscape(token)))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		r.AddCookie(sess)
		return do(h, r)
	}

	if resp := logout("https://files.example.com", csrf); resp.StatusCode != http.StatusForbidden {
		t.Errorf("other origin: got %d", resp.StatusCode)
	}
	if resp := logout(adminURL, "wrong"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong csrf: got %d", resp.StatusCode)
	}
	if resp := logout(adminURL, csrf); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("good logout: got %d", resp.StatusCode)
	}

	r = httptest.NewRequest("GET", "/", nil)
	r.AddCookie(sess)
	if resp := do(h, r); resp.Header.Get("Location") != "/login" {
		t.Error("session still works after logout")
	}
}

func TestLogoutAllEndsEverySession(t *testing.T) {
	h := newApp(t, ownerID)
	first := cookie(login(t, h, ""), sessionCookie)
	second := cookie(login(t, h, ""), sessionCookie)

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(second)
	m := csrfField.FindStringSubmatch(readBody(do(h, r)))
	if m == nil {
		t.Fatal("no csrf field on the Admin page")
	}

	post := func(origin string) *http.Response {
		r := httptest.NewRequest("POST", "/logout-all", strings.NewReader("csrf="+url.QueryEscape(m[1])))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		r.AddCookie(second)
		return do(h, r)
	}
	if resp := post("https://files.example.com"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("other origin: got %d", resp.StatusCode)
	}
	if resp := post(adminURL); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout-all: got %d", resp.StatusCode)
	}

	for _, c := range []*http.Cookie{first, second} {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(c)
		if resp := do(h, r); resp.Header.Get("Location") != "/login" {
			t.Error("a session still works after logout-all")
		}
	}
}

func TestNewLoginEndsOldSession(t *testing.T) {
	h := newApp(t, ownerID)
	old := cookie(login(t, h, ""), sessionCookie)

	// Log in again in the same browser, which still sends the old cookie.
	start := do(h, httptest.NewRequest("GET", "/auth/github", nil))
	loc, _ := url.Parse(start.Header.Get("Location"))
	r := httptest.NewRequest("GET", "/auth/github/callback?code=abc&state="+url.QueryEscape(loc.Query().Get("state")), nil)
	r.AddCookie(cookie(start, loginCookie))
	r.AddCookie(old)
	if cookie(do(h, r), sessionCookie) == nil {
		t.Fatal("no new session cookie")
	}

	r = httptest.NewRequest("GET", "/", nil)
	r.AddCookie(old)
	if resp := do(h, r); resp.Header.Get("Location") != "/login" {
		t.Error("old session still works after a new login")
	}
}

// fromSite marks a request as an <img> on a Site of the same site.
func fromSite(r *http.Request) *http.Request {
	r.Header.Set("Sec-Fetch-Site", "same-site")
	r.Header.Set("Sec-Fetch-Mode", "no-cors")
	r.Header.Set("Sec-Fetch-Dest", "image")
	return r
}

func TestOnlyPageVisitsRenew(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sessions := auth.NewSessions(db)
	h := New(owner, 1, adminURL, filesURL, &fakeGitHub{id: ownerID}, sessions, auth.NewCLI(db), sites.New(db, dir)).Handler()
	sess := cookie(login(t, h, ""), sessionCookie)

	later := time.Now().Add(2 * 24 * time.Hour)
	sessions.Now = func() time.Time { return later }

	r := fromSite(httptest.NewRequest("GET", "/", nil))
	r.AddCookie(sess)
	if c := cookie(do(h, r), sessionCookie); c != nil {
		t.Error("a request from a Site renewed the session")
	}

	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Sec-Fetch-Site", "none")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	r.Header.Set("Sec-Fetch-Dest", "document")
	r.AddCookie(sess)
	if c := cookie(do(h, r), sessionCookie); c == nil {
		t.Error("a page visit did not renew the session")
	}
}

func TestSiteCannotTouchLoginCookie(t *testing.T) {
	h := newApp(t, ownerID)
	for _, path := range []string{"/auth/github", "/auth/github/callback?error=access_denied"} {
		resp := do(h, fromSite(httptest.NewRequest("GET", path, nil)))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: got %d", path, resp.StatusCode)
		}
		if cookie(resp, loginCookie) != nil {
			t.Errorf("%s: changed the login cookie", path)
		}
	}
}

func readBody(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
