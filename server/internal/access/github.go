package access

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

// Identity is what a login provider tells us about the person.
type Identity struct {
	ID        int64
	Login     string
	AvatarURL string
	Emails    []Email
}

type Email struct {
	Address  string
	Verified bool
}

// Provider is the seam for a login provider. GitHub is the only one in v1.
// Tests use a fake, and Google can be a second adapter later.
type Provider interface {
	// LoginURL is the address that starts the login. The verifier is the
	// PKCE secret; only its S256 hash goes in the URL.
	LoginURL(state, verifier string) string
	// Finish swaps the code from the callback for the person's identity.
	Finish(ctx context.Context, code, verifier string) (Identity, error)
}

type GitHub struct {
	oauth *oauth2.Config
}

func NewGitHub(clientID, clientSecret, redirectURL string) *GitHub {
	return &GitHub{oauth: &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     github.Endpoint,
		RedirectURL:  redirectURL,
		Scopes:       []string{"user:email"},
	}}
}

func (g *GitHub) LoginURL(state, verifier string) string {
	return g.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

func (g *GitHub) Finish(ctx context.Context, code, verifier string) (Identity, error) {
	tok, err := g.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("github exchange: %w", err)
	}
	client := g.oauth.Client(ctx, tok)

	var user struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user", &user); err != nil {
		return Identity{}, err
	}

	var emails []struct {
		Email    string `json:"email"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user/emails", &emails); err != nil {
		return Identity{}, err
	}

	id := Identity{ID: user.ID, Login: user.Login, AvatarURL: user.AvatarURL}
	for _, e := range emails {
		id.Emails = append(id.Emails, Email{Address: e.Email, Verified: e.Verified})
	}
	return id, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("github %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
