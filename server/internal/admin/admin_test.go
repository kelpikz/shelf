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

	"github.com/kelpikz/shelf/server/internal/access"
	"github.com/kelpikz/shelf/server/internal/store"
)

const (
	adminURL = "https://files-admin.example.com"
	owner    = "owner@example.com"
)

// fakeGitHub gives back a fixed identity and checks the PKCE verifier.
type fakeGitHub struct {
	id       access.Identity
	verifier string
}

func (f *fakeGitHub) LoginURL(state, verifier string) string {
	f.verifier = verifier
	return "https://github.example/authorize?state=" + url.QueryEscape(state)
}

func (f *fakeGitHub) Finish(_ context.Context, code, verifier string) (access.Identity, error) {
	if verifier != f.verifier {
		return access.Identity{}, context.Canceled
	}
	return f.id, nil
}

func newApp(t *testing.T, id access.Identity) http.Handler {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(owner, adminURL, &fakeGitHub{id: id}, access.NewSessions(db)).Handler()
}

var ownerID = access.Identity{ID: 1, Login: "ajitha", Emails: []access.Email{{Address: owner, Verified: true}}}

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
	stranger := access.Identity{ID: 2, Login: "stranger", Emails: []access.Email{{Address: owner, Verified: false}}}
	resp := login(t, newApp(t, stranger), "")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(readBody(resp), "Thanks") {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if cookie(resp, sessionCookie) != nil {
		t.Error("stranger got a session cookie")
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
		t.Fatal("no csrf field on the admin page")
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

func readBody(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
