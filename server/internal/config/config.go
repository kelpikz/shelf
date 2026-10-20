// Package config reads the server settings from env variables.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/kelpikz/shelf/server/internal/utils"
)

type Config struct {
	OwnerEmail         string
	OwnerGitHubID      int64 // the number of the Owner's GitHub account; it never changes
	GitHubClientID     string
	GitHubClientSecret string
	// AdminURL is the origin of the admin host, for example
	// "https://files-admin.ajitha.fyi". It has no path and no trailing slash.
	AdminURL string
	DataDir  string
	Port     string
}

// Load reads the config. Values from a local .env file are used only when the
// env variable is not already set.
func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}

	c := Config{
		OwnerEmail:         os.Getenv("OWNER_EMAIL"),
		GitHubClientID:     os.Getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
		AdminURL:           strings.TrimSuffix(os.Getenv("ADMIN_URL"), "/"),
		DataDir:            envOr("DATA_DIR", "data"),
		Port:               envOr("PORT", "8080"),
	}

	ownerID := os.Getenv("OWNER_GITHUB_ID")

	var missing []string
	for name, v := range map[string]string{
		"OWNER_EMAIL":          c.OwnerEmail,
		"OWNER_GITHUB_ID":      ownerID,
		"GITHUB_CLIENT_ID":     c.GitHubClientID,
		"GITHUB_CLIENT_SECRET": c.GitHubClientSecret,
		"ADMIN_URL":            c.AdminURL,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing env variables: %s", strings.Join(missing, ", "))
	}

	u, err := url.Parse(c.AdminURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.ContainsAny(c.AdminURL, "#") {
		return Config{}, fmt.Errorf("ADMIN_URL must look like https://files-admin.example.com, got %q", c.AdminURL)
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		log.Printf("warning: ADMIN_URL uses http, not https: %s", c.AdminURL)
	}

	// The Owner check compares ASCII only (see access.IsOwner).
	if !utils.IsASCII(c.OwnerEmail) || strings.Count(c.OwnerEmail, "@") != 1 {
		return Config{}, fmt.Errorf("OWNER_EMAIL must be one plain ASCII email address, got %q", c.OwnerEmail)
	}

	c.OwnerGitHubID, err = strconv.ParseInt(ownerID, 10, 64)
	if err != nil || c.OwnerGitHubID <= 0 {
		return Config{}, fmt.Errorf("OWNER_GITHUB_ID must be the number of a GitHub account, got %q", ownerID)
	}

	return c, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
	return s.Err()
}
