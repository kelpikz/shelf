package admin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/store"
)

func oauthApp(t *testing.T) (http.Handler, *http.Cookie, string) {
	return oauthAppAs(t, ownerID)
}

func oauthAppAs(t *testing.T, identity auth.Identity) (http.Handler, *http.Cookie, string) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sessions := auth.NewSessions(db)
	token, sess, err := sessions.Create(context.Background(), auth.OwnerProfile{DisplayName: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	a := New(owner, 1, adminURL, filesURL, &fakeGitHub{id: identity}, sessions, auth.NewCLI(db), nil)
	a.OAuth = auth.NewOAuth(db)
	a.MCPResource = "http://localhost:8081/mcp"
	a.MCPClientID = "shelf-mcp"
	a.MCPRedirectURIs = []string{"http://localhost:8091/callback"}
	return a.Handler(), &http.Cookie{Name: sessionCookie, Value: token}, sess.CSRF
}

func authorizationQuery() url.Values {
	sum := sha256.Sum256([]byte(strings.Repeat("v", 43)))
	return url.Values{"response_type": {"code"}, "client_id": {"shelf-mcp"}, "redirect_uri": {"http://localhost:8091/callback"}, "resource": {"http://localhost:8081/mcp"}, "scope": {"shelf:read"}, "state": {"test-state"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
}

func formRequest(path string, values url.Values) *http.Request {
	r := httptest.NewRequest("POST", adminURL+path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestOAuthRequiresOwnerApprovalAndRejectsUnsafeRequests(t *testing.T) {
	h, c, csrf := oauthApp(t)
	for _, field := range []string{"redirect_uri", "client_id", "resource", "code_challenge_method", "scope"} {
		q := authorizationQuery()
		q.Set(field, "https://evil.example")
		resp := do(h, httptest.NewRequest("GET", adminURL+"/oauth/authorize?"+q.Encode(), nil))
		if resp.StatusCode != 400 || resp.Header.Get("Location") != "" {
			t.Fatalf("%s: status %d location %q", field, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	q := authorizationQuery()
	q.Add("redirect_uri", "https://evil.example")
	if resp := do(h, httptest.NewRequest("GET", adminURL+"/oauth/authorize?"+q.Encode(), nil)); resp.StatusCode != 400 {
		t.Fatal("duplicate parameter accepted")
	}
	resp := do(h, httptest.NewRequest("GET", adminURL+"/oauth/authorize?"+authorizationQuery().Encode(), nil))
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/login" {
		t.Fatalf("no Owner: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	next := cookie(resp, oauthNextCookie)
	if next == nil {
		t.Fatal("login continuation missing")
	}
	loginReq := httptest.NewRequest("GET", adminURL+"/login", nil)
	loginReq.AddCookie(c)
	loginReq.AddCookie(next)
	resp = do(h, loginReq)
	if !strings.HasPrefix(resp.Header.Get("Location"), "/oauth/authorize?request=") {
		t.Fatal("login did not resume OAuth")
	}
	request := httptest.NewRequest("GET", adminURL+resp.Header.Get("Location"), nil)
	request.AddCookie(c)
	resp = do(h, request)
	body, _ := io.ReadAll(resp.Body)
	match := regexp.MustCompile(`name="request" value="([^"]+)"`).FindSubmatch(body)
	if resp.StatusCode != 200 || len(match) != 2 {
		t.Fatalf("approval page: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "form-action 'self' http://localhost:8091;") {
		t.Fatal("approval policy blocks the callback redirect")
	}
	values := url.Values{"request": {string(match[1])}, "csrf": {csrf}, "decision": {"approve"}}
	bad := formRequest("/oauth/authorize", values)
	bad.AddCookie(c)
	if do(h, bad).StatusCode != 403 {
		t.Fatal("missing Origin accepted")
	}
	approved := formRequest("/oauth/authorize", values)
	approved.Header.Set("Origin", adminURL)
	approved.AddCookie(c)
	resp = do(h, approved)
	redirect, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 303 || redirect.Query().Get("code") == "" || redirect.Query().Get("state") != "test-state" {
		t.Fatalf("approval: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	values = url.Values{"grant_type": {"authorization_code"}, "client_id": {"shelf-mcp"}, "code": {redirect.Query().Get("code")}, "redirect_uri": {"http://localhost:8091/callback"}, "resource": {"http://localhost:8081/mcp"}, "code_verifier": {strings.Repeat("v", 43)}}
	resp = do(h, formRequest("/oauth/token", values))
	var tokens auth.OAuthTokens
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("token: %d", resp.StatusCode)
	}
	check := httptest.NewRequest("GET", adminURL+"/oauth/session?resource="+url.QueryEscape("http://localhost:8081/mcp"), nil)
	check.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	resp = do(h, check)
	if resp.StatusCode != 200 {
		t.Fatalf("session: %d", resp.StatusCode)
	}
	connections := httptest.NewRequest("GET", adminURL+"/oauth/connections", nil)
	connections.AddCookie(c)
	page := do(h, connections)
	contents, _ := io.ReadAll(page.Body)
	grant := regexp.MustCompile(`/oauth/connections/([A-Za-z0-9_-]+)/revoke`).FindSubmatch(contents)
	if page.StatusCode != 200 || len(grant) != 2 {
		t.Fatalf("connections page missing grant: %d", page.StatusCode)
	}
	path := "/oauth/connections/" + string(grant[1]) + "/revoke"
	wrongOrigin := formRequest(path, url.Values{"csrf": {csrf}})
	wrongOrigin.AddCookie(c)
	wrongOrigin.Header.Set("Origin", "https://evil.example")
	if do(h, wrongOrigin).StatusCode != 403 {
		t.Fatal("cross-origin revocation accepted")
	}
	ownerRevoke := formRequest(path, url.Values{"csrf": {csrf}})
	ownerRevoke.AddCookie(c)
	ownerRevoke.Header.Set("Origin", adminURL)
	if do(h, ownerRevoke).StatusCode != 303 {
		t.Fatal("Owner could not revoke grant")
	}
	if do(h, check).StatusCode != 401 {
		t.Fatal("browser revocation did not invalidate token")
	}
	if do(h, formRequest("/oauth/token", values)).StatusCode != 400 {
		t.Fatal("code replay accepted")
	}
	resp = do(h, formRequest("/oauth/revoke", url.Values{"client_id": {"shelf-mcp"}, "token": {tokens.RefreshToken}}))
	if resp.StatusCode != 200 {
		t.Fatal("revoke failed")
	}
	if do(h, check).StatusCode != 401 {
		t.Fatal("revoked token accepted")
	}
}

func TestOAuthDiscoveryAndAccessIsolation(t *testing.T) {
	h, c, _ := oauthApp(t)
	resp := do(h, httptest.NewRequest("GET", adminURL+"/.well-known/oauth-authorization-server", nil))
	var metadata map[string]any
	json.NewDecoder(resp.Body).Decode(&metadata)
	if resp.StatusCode != 200 || metadata["issuer"] != adminURL {
		t.Fatalf("metadata: %d %v", resp.StatusCode, metadata)
	}
	r := httptest.NewRequest("GET", adminURL+"/oauth/session?resource=http://localhost:8081/mcp", nil)
	r.AddCookie(c)
	if resp = do(h, r); resp.StatusCode != 401 {
		t.Fatal("browser cookie accepted as OAuth token")
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatal("discovery challenge missing")
	}
}

func TestOAuthConsentKeepsBrowserFormOriginAndRejectsUntrustedPosts(t *testing.T) {
	h, c, csrf := oauthApp(t)
	r := httptest.NewRequest("GET", adminURL+"/oauth/authorize?"+authorizationQuery().Encode(), nil)
	r.AddCookie(c)
	page := do(h, r)
	body, err := io.ReadAll(page.Body)
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`name="request" value="([^"]+)"`).FindSubmatch(body)
	if page.StatusCode != 200 || len(match) != 2 {
		t.Fatal("consent page missing")
	}
	// Per Fetch's Origin-header algorithm, no-referrer makes a browser's
	// navigate-mode form POST send Origin: null, even to the same origin.
	origin := adminURL
	if page.Header.Get("Referrer-Policy") == "no-referrer" {
		origin = "null"
	}
	values := url.Values{"request": {string(match[1])}, "csrf": {csrf}, "decision": {"approve"}}
	for _, badOrigin := range []string{"null", "https://evil.example", filesURL} {
		post := formRequest("/oauth/authorize", values)
		post.AddCookie(c)
		post.Header.Set("Origin", badOrigin)
		post.Header.Set("Sec-Fetch-Site", "same-origin")
		if resp := do(h, post); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("accepted origin %q: %d", badOrigin, resp.StatusCode)
		}
	}
	wrongCSRF := formRequest("/oauth/authorize", url.Values{"request": {string(match[1])}, "csrf": {"wrong"}, "decision": {"approve"}})
	wrongCSRF.AddCookie(c)
	wrongCSRF.Header.Set("Origin", adminURL)
	if resp := do(h, wrongCSRF); resp.StatusCode != http.StatusForbidden {
		t.Fatal("wrong CSRF token accepted")
	}
	post := formRequest("/oauth/authorize", values)
	post.AddCookie(c)
	post.Header.Set("Origin", origin)
	post.Header.Set("Sec-Fetch-Site", "same-origin")
	resp := do(h, post)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("browser approval origin %q: got %d, want 303", origin, resp.StatusCode)
	}
	redirect, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || redirect.Query().Get("code") == "" {
		t.Fatal("authorization code missing")
	}
	if resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("callback redirect must not leak its authorization code through Referer")
	}
}

func TestOAuthGitHubLoginResumesApprovalOnlyForOwner(t *testing.T) {
	for _, test := range []struct {
		name     string
		identity auth.Identity
		status   int
	}{
		{"Owner", ownerID, 303},
		{"Visitor", auth.Identity{ID: 2, Login: "visitor", Emails: []auth.Email{{Address: owner, Verified: true}}}, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, _, _ := oauthAppAs(t, test.identity)
			pending := do(h, httptest.NewRequest("GET", adminURL+"/oauth/authorize?"+authorizationQuery().Encode(), nil))
			next := cookie(pending, oauthNextCookie)
			if next == nil {
				t.Fatal("continuation missing")
			}
			start := do(h, httptest.NewRequest("GET", adminURL+"/auth/github", nil))
			providerURL, err := url.Parse(start.Header.Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			callback := httptest.NewRequest("GET", adminURL+"/auth/github/callback?code=test&state="+url.QueryEscape(providerURL.Query().Get("state")), nil)
			callback.AddCookie(cookie(start, loginCookie))
			callback.AddCookie(next)
			resp := do(h, callback)
			if resp.StatusCode != test.status {
				t.Fatalf("status=%d", resp.StatusCode)
			}
			if test.status == 403 {
				if cookie(resp, sessionCookie) != nil {
					t.Fatal("Visitor got an Owner session")
				}
				return
			}
			if !strings.HasPrefix(resp.Header.Get("Location"), "/oauth/authorize?request=") {
				t.Fatal("login lost the approval request")
			}
			approval := httptest.NewRequest("GET", adminURL+resp.Header.Get("Location"), nil)
			approval.AddCookie(cookie(resp, sessionCookie))
			page := do(h, approval)
			body, _ := io.ReadAll(page.Body)
			if page.StatusCode != 200 || !strings.Contains(string(body), "Approve this connection?") {
				t.Fatal("Owner approval missing")
			}
		})
	}
}
