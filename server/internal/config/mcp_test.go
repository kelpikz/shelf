package config

import "testing"

func TestMCPConfigRequiresExactSafeURLs(t *testing.T) {
	c := Config{AdminURL: "http://localhost:8080"}
	if err := configureMCP(&c, "http://localhost:8081/mcp", "shelf-mcp", "http://localhost:8091/callback"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ resource, client, redirect string }{
		{"http://remote.example/mcp", "local", "http://localhost:8091/callback"},
		{"https://mcp.example/mcp?x=1", "local", "http://localhost:8091/callback"},
		{"https://mcp.example/mcp", "local", "https://app.example/callback?x=1"},
		{"https://mcp.example/mcp", "local", "https://app.example/callback#fragment"},
		{"https://mcp.example/mcp", "local", "http://localhost.evil.example/callback"},
		{"https://mcp.example/mcp", "local", ""},
		{"https://mcp.example/mcp", "", "https://app.example/callback"},
	} {
		if err := configureMCP(&c, test.resource, test.client, test.redirect); err == nil {
			t.Errorf("accepted %+v", test)
		}
	}
}
