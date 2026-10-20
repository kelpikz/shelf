// Package config reads the server settings from env variables.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
)

type Config struct {
	OwnerEmail         string
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

	var missing []string
	for name, v := range map[string]string{
		"OWNER_EMAIL":          c.OwnerEmail,
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
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
		return Config{}, fmt.Errorf("ADMIN_URL must look like https://files-admin.example.com, got %q", c.AdminURL)
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
