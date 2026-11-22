package config

import (
	"fmt"
	"net/url"
	"strings"
)

func configureMCP(c *Config, resource, client, redirects string) error {
	if resource == "" {
		return nil
	}
	u, err := url.Parse(resource)
	if err != nil || !safeOAuthURL(u) || u.Path != "/mcp" {
		return fmt.Errorf("MCP_RESOURCE_URL must be an HTTPS URL ending in /mcp; only loopback hosts can use HTTP")
	}
	if len(client) == 0 || len(client) > 128 || strings.ContainsAny(client, "\r\n\t ") {
		return fmt.Errorf("MCP_CLIENT_ID must have 1 to 128 characters without whitespace")
	}
	if redirects == "" {
		return fmt.Errorf("MCP_REDIRECT_URIS must list the client's exact callback URLs")
	}
	var allowed []string
	for _, raw := range strings.Split(redirects, ",") {
		raw = strings.TrimSpace(raw)
		r, e := url.Parse(raw)
		if e != nil || !safeOAuthURL(r) || r.Path == "" {
			return fmt.Errorf("MCP_REDIRECT_URIS must contain HTTPS callback URLs; only loopback hosts can use HTTP")
		}
		allowed = append(allowed, raw)
	}
	issuer, err := url.Parse(c.AdminURL)
	if err != nil || !safeOAuthURL(issuer) {
		return fmt.Errorf("MCP login requires HTTPS ADMIN_URL; only loopback hosts can use HTTP")
	}
	c.MCPResourceURL, c.MCPClientID, c.MCPRedirectURIs = resource, client, allowed
	return nil
}

func safeOAuthURL(u *url.URL) bool {
	if u == nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(u.String(), "#") {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))
}
