package admin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/kelpikz/shelf/server/internal/auth"
)

const oauthNextCookie = "__Host-shelf_oauth_next"

func loginContinuation(r *http.Request) string {
	if c, err := r.Cookie(oauthNextCookie); err == nil && validOAuthSecret(c.Value, 43, 43) {
		return "/oauth/authorize?request=" + url.QueryEscape(c.Value)
	}
	return viewContinuation(r)
}

func oauthJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func oauthError(w http.ResponseWriter, status int, code string) {
	oauthJSON(w, status, map[string]string{"error": code})
}

func (a *App) oauthMetadata(w http.ResponseWriter, r *http.Request) {
	oauthJSON(w, 200, map[string]any{
		"issuer": a.AdminURL, "authorization_endpoint": a.AdminURL + "/oauth/authorize",
		"token_endpoint": a.AdminURL + "/oauth/token", "revocation_endpoint": a.AdminURL + "/oauth/revoke",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		"revocation_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{"shelf:read"},
		"authorization_response_iss_parameter_supported": true,
	})
}

func (a *App) oauthResourceMetadata(w http.ResponseWriter, r *http.Request) {
	oauthJSON(w, 200, map[string]any{"resource": a.MCPResource, "authorization_servers": []string{a.AdminURL}, "scopes_supported": []string{"shelf:read"}, "bearer_methods_supported": []string{"header"}})
}

func uniqueValues(values url.Values) bool {
	for _, v := range values {
		if len(v) != 1 {
			return false
		}
	}
	return true
}

func validOAuthSecret(s string, minLen, maxLen int) bool {
	if len(s) < minLen || len(s) > maxLen {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}

func (a *App) validOAuthRequest(q url.Values) bool {
	challenge, err := base64.RawURLEncoding.DecodeString(q.Get("code_challenge"))
	return uniqueValues(q) && q.Get("response_type") == "code" && q.Get("client_id") == a.MCPClientID &&
		slices.Contains(a.MCPRedirectURIs, q.Get("redirect_uri")) && q.Get("resource") == a.MCPResource &&
		q.Get("scope") == "shelf:read" && q.Get("code_challenge_method") == "S256" && err == nil && len(challenge) == 32 && len(q.Get("code_challenge")) == 43 &&
		len(q.Get("state")) <= 512
}

func (a *App) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	if !isPageVisit(r) {
		http.Error(w, "Forbidden", 403)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !uniqueValues(q) || len(r.URL.RawQuery) > 4096 {
		oauthError(w, 400, "invalid_request")
		return
	}
	id := q.Get("request")
	if id == "" {
		if !a.validOAuthRequest(q) {
			oauthError(w, 400, "invalid_request")
			return
		}
		id, err = a.OAuth.Start(r.Context(), auth.OAuthRequest{ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), Resource: q.Get("resource"), Scope: q.Get("scope"), State: q.Get("state"), Challenge: q.Get("code_challenge")})
		if err != nil {
			a.serverError(w, err)
			return
		}
	} else if len(q) != 1 || !validOAuthSecret(id, 43, 43) {
		oauthError(w, 400, "invalid_request")
		return
	}
	pending, err := a.OAuth.Pending(r.Context(), id)
	if errors.Is(err, auth.ErrOAuthGrant) {
		oauthError(w, 400, "invalid_request")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	// Configuration changes must not keep old redirect or resource grants alive.
	if pending.ClientID != a.MCPClientID || pending.Resource != a.MCPResource || !slices.Contains(a.MCPRedirectURIs, pending.RedirectURI) {
		oauthError(w, 400, "invalid_request")
		return
	}
	sess, ok := a.session(w, r)
	if !ok {
		http.SetCookie(w, &http.Cookie{Name: oauthNextCookie, Value: id, Path: "/", MaxAge: 600, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/login", 303)
		return
	}
	// Keep the same-origin policy from securityHeaders. A no-referrer
	// consent page makes browser form POSTs send Origin: null.
	allowOAuthCallback(w, pending.RedirectURI)
	a.render(w, 200, "oauth.html", map[string]any{"Request": id, "Client": pending.ClientID, "Redirect": pending.RedirectURI, "CSRF": sess.CSRF})
}

func oauthForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.URL.RawQuery != "" || err != nil || mediaType != "application/x-www-form-urlencoded" || r.ParseForm() != nil || !uniqueValues(r.PostForm) {
		oauthError(w, 400, "invalid_request")
		return false
	}
	return true
}

func (a *App) oauthDecision(w http.ResponseWriter, r *http.Request) {
	if !oauthForm(w, r) {
		return
	}
	if _, ok := a.checkPost(w, r); !ok {
		return
	}
	id := r.PostForm.Get("request")
	if !validOAuthSecret(id, 43, 43) {
		oauthError(w, 400, "invalid_request")
		return
	}
	pending, err := a.OAuth.Pending(r.Context(), id)
	if errors.Is(err, auth.ErrOAuthGrant) {
		oauthError(w, 400, "invalid_request")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	if pending.ClientID != a.MCPClientID || pending.Resource != a.MCPResource || !slices.Contains(a.MCPRedirectURIs, pending.RedirectURI) {
		oauthError(w, 400, "invalid_request")
		return
	}
	result := url.Values{"state": {pending.State}, "iss": {a.AdminURL}}
	switch r.PostForm.Get("decision") {
	case "approve":
		code, _, err := a.OAuth.Approve(r.Context(), id)
		if err != nil {
			oauthError(w, 400, "invalid_request")
			return
		}
		result.Set("code", code)
	case "deny":
		if err := a.OAuth.Deny(r.Context(), id); err != nil {
			a.serverError(w, err)
			return
		}
		result.Set("error", "access_denied")
	default:
		oauthError(w, 400, "invalid_request")
		return
	}
	clearCookie(w, oauthNextCookie)
	w.Header().Set("Referrer-Policy", "no-referrer")
	allowOAuthCallback(w, pending.RedirectURI)
	target, _ := url.Parse(pending.RedirectURI)
	target.RawQuery = result.Encode()
	http.Redirect(w, r, target.String(), 303)
}

// Browsers can apply the consent page's form-action policy to a POST redirect.
// Allow only the configured callback origin for this one approval page.
func allowOAuthCallback(w http.ResponseWriter, callback string) {
	u, err := url.Parse(callback)
	if err != nil {
		return
	}
	origin := u.Scheme + "://" + u.Host
	policy := w.Header().Get("Content-Security-Policy")
	w.Header().Set("Content-Security-Policy", strings.Replace(policy, "form-action 'self';", "form-action 'self' "+origin+";", 1))
}

func (a *App) oauthToken(w http.ResponseWriter, r *http.Request) {
	if !oauthForm(w, r) {
		return
	}
	f := r.PostForm
	if f.Get("client_id") != a.MCPClientID || r.Header.Get("Authorization") != "" {
		oauthError(w, 400, "invalid_client")
		return
	}
	if f.Get("resource") != a.MCPResource {
		oauthError(w, 400, "invalid_target")
		return
	}
	var tokens auth.OAuthTokens
	var err error
	switch f.Get("grant_type") {
	case "authorization_code":
		if !validOAuthSecret(f.Get("code"), 43, 43) || !validOAuthSecret(f.Get("code_verifier"), 43, 128) || !slices.Contains(a.MCPRedirectURIs, f.Get("redirect_uri")) {
			oauthError(w, 400, "invalid_grant")
			return
		}
		tokens, err = a.OAuth.Exchange(r.Context(), f.Get("code"), f.Get("client_id"), f.Get("redirect_uri"), f.Get("resource"), f.Get("code_verifier"))
	case "refresh_token":
		tokens, err = a.OAuth.Refresh(r.Context(), f.Get("refresh_token"), f.Get("client_id"), f.Get("resource"))
	default:
		oauthError(w, 400, "unsupported_grant_type")
		return
	}
	if errors.Is(err, auth.ErrOAuthGrant) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	oauthJSON(w, 200, tokens)
}

func (a *App) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	if !oauthForm(w, r) {
		return
	}
	if r.PostForm.Get("client_id") != a.MCPClientID || r.Header.Get("Authorization") != "" {
		oauthError(w, 400, "invalid_client")
		return
	}
	if err := a.OAuth.Revoke(r.Context(), r.PostForm.Get("token"), a.MCPClientID); err != nil {
		a.serverError(w, err)
		return
	}
	oauthJSON(w, 200, map[string]any{})
}

// oauthSession checks an opaque token online. Only the holder can inspect it.
// This is not a CLI key endpoint and never accepts browser cookies.
func (a *App) oauthSession(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !uniqueValues(q) || len(q) != 1 || q.Get("resource") != a.MCPResource {
		oauthError(w, 400, "invalid_target")
		return
	}
	header := r.Header.Get("Authorization")
	scheme, token, ok := strings.Cut(header, " ")
	if len(r.Header.Values("Authorization")) != 1 || !ok || !strings.EqualFold(scheme, "Bearer") || len(token) > 256 || token == "" {
		a.oauthChallenge(w)
		return
	}
	session, err := a.OAuth.Check(r.Context(), token, a.MCPResource)
	if errors.Is(err, auth.ErrOAuthGrant) {
		a.oauthChallenge(w)
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	oauthJSON(w, 200, map[string]any{"iss": a.AdminURL, "sub": strconv.FormatInt(a.OwnerGitHubID, 10), "aud": session.Resource, "scope": session.Scope, "expires_at": session.ExpiresAt})
}

func (a *App) oauthChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+a.AdminURL+`/.well-known/oauth-protected-resource", scope="shelf:read"`)
	oauthError(w, 401, "invalid_token")
}
