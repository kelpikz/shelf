package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/config"
	"github.com/kelpikz/shelf/server/internal/server"
	"github.com/kelpikz/shelf/server/internal/sites"
	"github.com/kelpikz/shelf/server/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	db, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	allSites, err := prepareSites(context.Background(), db, cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}

	github := auth.NewGitHub(cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.AdminURL+"/auth/github/callback")
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server.New(cfg, github, auth.NewSessions(db), auth.NewCLI(db), allSites),
		ReadHeaderTimeout: 10 * time.Second,
		// The uploads will need more time. Give it to the upload handler
		// with http.ResponseController, not here.
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Printf("shelf server listening on %s (sites: %s, admin: %s)", srv.Addr, cfg.FilesURL, cfg.AdminURL)
	log.Fatal(srv.ListenAndServe())
}

func prepareSites(ctx context.Context, db *sql.DB, dataDir string) (*sites.Sites, error) {
	allSites := sites.New(db, dataDir)
	// Cleanup must finish before uploads can start.
	if err := allSites.Cleanup(ctx); err != nil {
		return nil, err
	}
	return allSites, nil
}
