package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kelpikz/shelf/server/internal/access"
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

	// "server logout-all" ends every Admin page session. Use it when no
	// browser is logged in, for example after a lost laptop.
	if len(os.Args) > 1 && os.Args[1] == "logout-all" {
		n, err := access.NewSessions(db).DeleteAll(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Ended %d Admin page sessions.\n", n)
		return
	}

	allSites := sites.New(db, cfg.DataDir)

	if err := allSites.Cleanup(context.Background()); err != nil {
		log.Fatal(err)
	}

	github := access.NewGitHub(cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.AdminURL+"/auth/github/callback")
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server.New(cfg, github, access.NewSessions(db), allSites),
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
